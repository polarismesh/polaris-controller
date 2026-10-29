// Tencent is pleased to support the open source community by making Polaris available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	localCache "github.com/polarismesh/polaris-controller/pkg/cache"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

type testConsumer struct {
	api.ConsumerAPI
	instances []model.Instance
	err       error
	calls     int
}

func (c *testConsumer) GetAllInstances(*api.GetAllInstancesRequest) (*model.InstancesResponse, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &model.InstancesResponse{Instances: c.instances}, nil
}

type testInstance struct {
	model.Instance
	host              string
	metadata          map[string]string
	weight            int
	protocol          string
	version           string
	enableHealthCheck bool
	ttl               int64
}

func (i testInstance) GetMetadata() map[string]string { return i.metadata }
func (i testInstance) IsHealthy() bool                { return true }
func (i testInstance) IsIsolated() bool               { return false }
func (i testInstance) IsEnableHealthCheck() bool      { return i.enableHealthCheck }
func (i testInstance) GetTtl() int64                  { return i.ttl }
func (i testInstance) GetHost() string                { return i.host }
func (i testInstance) GetPort() uint32                { return 80 }
func (i testInstance) GetWeight() int                 { return i.weight }
func (i testInstance) GetProtocol() string            { return i.protocol }
func (i testInstance) GetVersion() string             { return i.version }

func ownedInstance(host string) testInstance {
	return testInstance{host: host, weight: util.DefaultWeight, protocol: string(v1.ProtocolTCP),
		metadata: map[string]string{
			util.PolarisSource:      Source,
			util.PolarisClusterName: "test",
			util.K8sServiceUID:      "",
		}}
}

type testStores struct{ services, endpoints, pods, namespaces, configs cache.Indexer }

func newTestController(t *testing.T) (*PolarisController, testStores) {
	t.Helper()
	newStore := func() cache.Indexer {
		return cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	}
	stores := testStores{newStore(), newStore(), newStore(), newStore(), newStore()}
	q := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	client := fake.NewSimpleClientset()
	t.Cleanup(q.ShutDown)
	p := &PolarisController{
		client: client,
		queue:  q, consumer: &testConsumer{}, eventRecorder: record.NewFakeRecorder(100),
		syncStateStore:      newSyncStateStore(client),
		serviceLister:       corelisters.NewServiceLister(stores.services),
		endpointsLister:     corelisters.NewEndpointsLister(stores.endpoints),
		podLister:           corelisters.NewPodLister(stores.pods),
		namespaceLister:     corelisters.NewNamespaceLister(stores.namespaces),
		configMapLister:     corelisters.NewConfigMapLister(stores.configs),
		appliedServiceCache: localCache.NewCachedServiceMap(), resyncServiceCache: localCache.NewCachedServiceMap(),
		configFileCache: localCache.NewCachedConfigFileMap(), resyncConfigFileCache: localCache.NewCachedConfigFileMap(),
	}
	p.config.PolarisController.ClusterName = "test"
	p.config.PolarisController.Size = 100
	p.config.PolarisController.SyncMode = util.SyncModeDemand
	p.config.PolarisController.ConfigSync = &options.ConfigSync{
		Enable: true, SyncDirection: util.SyncDirectionBoth, AllowDelete: true,
	}
	p.isPolarisServerHealthy.Store(true)
	return p, stores
}

func storeObject(t *testing.T, indexer cache.Indexer, obj interface{}) {
	t.Helper()
	if err := indexer.Add(obj); err != nil {
		t.Fatal(err)
	}
}

func takeQueueTask(t *testing.T, p *PolarisController) (*Task, taskQueueKey) {
	t.Helper()
	value, shutdown := p.queue.Get()
	if shutdown {
		t.Fatal("queue unexpectedly shut down")
	}
	defer p.queue.Done(value)
	key, ok := value.(taskQueueKey)
	if !ok {
		t.Fatalf("unexpected queue item type %T", value)
	}
	return key.task(), key
}

func testService() *v1.Service {
	return &v1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc",
		Annotations: map[string]string{util.PolarisSync: "true"}},
		Spec: v1.ServiceSpec{Selector: map[string]string{"app": "test"}, Ports: []v1.ServicePort{{Port: 80}}}}
}

func addTestEndpoint(t *testing.T, stores testStores, ips ...string) {
	t.Helper()
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod"}}
	storeObject(t, stores.pods, pod)
	ep := &v1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"}}
	subset := v1.EndpointSubset{Ports: []v1.EndpointPort{{Name: "http", Port: 80, Protocol: v1.ProtocolTCP}}}
	for _, ip := range ips {
		subset.Addresses = append(subset.Addresses, v1.EndpointAddress{IP: ip,
			TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "pod", Kind: "Pod"}})
	}
	ep.Subsets = []v1.EndpointSubset{subset}
	storeObject(t, stores.endpoints, ep)
}

type capturedRequest struct {
	method, path string
	body         []byte
}
type testTransport struct {
	sync.Mutex
	requests []capturedRequest
	respond  func(*http.Request) (*http.Response, error)
}

func (tr *testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	tr.Lock()
	tr.requests = append(tr.requests, capturedRequest{r.Method, r.URL.Path, body})
	tr.Unlock()
	if tr.respond != nil {
		return tr.respond(r)
	}
	return successResponse(), nil
}

func successResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{"code":200000,"info":"success","services":[{"name":"svc","namespace":"ns","metadata":{}}]}`))}
}

func mockPolarisHTTP(t *testing.T) *testTransport {
	t.Helper()
	client, url, token, operator := http.DefaultClient, polarisapi.PolarisHttpURL, polarisapi.PolarisAccessToken, polarisapi.PolarisOperator
	t.Cleanup(func() {
		http.DefaultClient, polarisapi.PolarisHttpURL, polarisapi.PolarisAccessToken, polarisapi.PolarisOperator = client, url, token, operator
	})
	tr := &testTransport{}
	http.DefaultClient = &http.Client{Transport: tr}
	polarisapi.PolarisHttpURL, polarisapi.PolarisAccessToken, polarisapi.PolarisOperator = "http://polaris.invalid", "test-token", ""
	return tr
}
