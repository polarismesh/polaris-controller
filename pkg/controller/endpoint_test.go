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
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestInstanceWriteFailuresPropagate(t *testing.T) {
	for _, operation := range []string{"add", "update", "delete", "all", "success"} {
		t.Run(operation, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			if operation != "success" {
				tr.respond = func(r *http.Request) (*http.Response, error) {
					return nil, errors.New("injected " + r.Method + " " + r.URL.Path)
				}
			}
			consumer := p.consumer.(*testConsumer)
			switch operation {
			case "add", "success":
				addTestEndpoint(t, stores, "10.0.0.1")
			case "update":
				addTestEndpoint(t, stores, "10.0.0.1")
				instance := ownedInstance("10.0.0.1")
				instance.weight = 1
				consumer.instances = []model.Instance{instance}
			case "delete":
				addTestEndpoint(t, stores)
				consumer.instances = []model.Instance{ownedInstance("10.0.0.1")}
			case "all":
				addTestEndpoint(t, stores, "10.0.0.1", "10.0.0.2")
				instance := ownedInstance("10.0.0.1")
				instance.weight = 1
				consumer.instances = []model.Instance{instance, ownedInstance("10.0.0.3")}
			}
			err := p.processSyncInstance(testService())
			if operation == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("write failure was swallowed")
			}
			wantCalls := 1
			if operation == "all" {
				wantCalls = 3
				for _, message := range []string{"POST /naming/v1/instances", "PUT /naming/v1/instances", "POST /naming/v1/instances/delete"} {
					if !strings.Contains(err.Error(), message) {
						t.Fatalf("aggregate missing %q: %v", message, err)
					}
				}
			}
			if len(tr.requests) != wantCalls {
				t.Fatalf("requests=%d want=%d", len(tr.requests), wantCalls)
			}
		})
	}
}

func TestEnableRegisterActsAsServiceSync(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	svc := testService()
	delete(svc.Annotations, util.PolarisSync)
	svc.Annotations[util.PolarisEnableRegister] = "true"
	if !p.IsPolarisService(svc) {
		t.Fatal("enableRegister=true did not enable service synchronization")
	}
	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 1 || tr.requests[0].method != http.MethodPost ||
		tr.requests[0].path != "/naming/v1/instances" {
		t.Fatalf("enableRegister=true did not register instances: %v", tr.requests)
	}
}

func TestEnableRegisterEndpointUpdateEnqueued(t *testing.T) {
	p, stores := newTestController(t)
	svc := testService()
	delete(svc.Annotations, util.PolarisSync)
	svc.Annotations[util.PolarisEnableRegister] = "true"
	storeObject(t, stores.services, svc)

	old := &v1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"}}
	cur := old.DeepCopy()
	cur.Subsets = []v1.EndpointSubset{{
		Addresses: []v1.EndpointAddress{{IP: "10.0.0.1"}},
		Ports:     []v1.EndpointPort{{Port: 80, Protocol: v1.ProtocolTCP}},
	}}
	p.onEndpointUpdate(old, cur)
	if p.queue.Len() != 1 {
		t.Fatal("enableRegister=true did not enqueue endpoint updates")
	}
}

func TestExplicitSyncFalseOverridesEnableRegister(t *testing.T) {
	p, _ := newTestController(t)
	svc := testService()
	svc.Annotations[util.PolarisSync] = "false"
	svc.Annotations[util.PolarisEnableRegister] = "true"
	if p.IsPolarisService(svc) {
		t.Fatal("explicit sync=false must override enableRegister=true")
	}
}

func TestEnableRegisterToggleReconciles(t *testing.T) {
	t.Run("enable", func(t *testing.T) {
		p, stores := newTestController(t)
		tr := mockPolarisHTTP(t)
		addTestEndpoint(t, stores, "10.0.0.1")
		old := testService()
		delete(old.Annotations, util.PolarisSync)
		cur := old.DeepCopy()
		cur.Annotations[util.PolarisEnableRegister] = "true"
		storeObject(t, stores.services, cur)
		key := (&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}).QueueKey()
		p.onServiceUpdate(old, cur)
		if p.queue.Len() != 1 {
			t.Fatal("enableRegister change not enqueued")
		}
		p.handleTask(p.process)
		if p.queue.NumRequeues(key) != 0 {
			t.Fatal("unexpected retry")
		}
		if countRequests(tr.requests, http.MethodPost, "/naming/v1/instances") != 1 {
			t.Fatalf("instance was not registered: %v", tr.requests)
		}
	})

	t.Run("disable", func(t *testing.T) {
		p, stores := newTestController(t)
		tr := mockPolarisHTTP(t)
		addTestEndpoint(t, stores)
		old := testService()
		delete(old.Annotations, util.PolarisSync)
		old.Annotations[util.PolarisEnableRegister] = "true"
		cur := old.DeepCopy()
		delete(cur.Annotations, util.PolarisEnableRegister)
		storeObject(t, stores.services, cur)
		p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}
		task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
		key := task.QueueKey()
		p.appliedServiceCache.Store(task.Key(), old)
		p.onServiceUpdate(old, cur)
		if p.queue.Len() != 1 {
			t.Fatal("enableRegister removal not enqueued")
		}
		p.handleTask(p.process)
		if p.queue.NumRequeues(key) != 0 {
			t.Fatal("unexpected retry")
		}
		if countRequests(tr.requests, http.MethodPost, "/naming/v1/instances/delete") != 1 {
			t.Fatalf("instance was not removed: %v", tr.requests)
		}
	})
}

func countRequests(requests []capturedRequest, method, path string) int {
	count := 0
	for _, request := range requests {
		if request.method == method && request.path == path {
			count++
		}
	}
	return count
}

func TestInstanceProtocolConvergesFromEndpointPort(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	svc := testService()
	storeObject(t, stores.services, svc)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod"}}
	storeObject(t, stores.pods, pod)
	longPortName := strings.Repeat("p", 64)
	ep := &v1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"},
		Subsets: []v1.EndpointSubset{{
			Addresses: []v1.EndpointAddress{{IP: "10.0.0.1",
				TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "pod", Kind: "Pod"}}},
			Ports: []v1.EndpointPort{{Name: longPortName, Port: 80, Protocol: v1.ProtocolTCP}},
		}}}
	storeObject(t, stores.endpoints, ep)
	current := ownedInstance("10.0.0.1")
	current.protocol = longPortName + "/TCP"
	consumer := p.consumer.(*testConsumer)
	consumer.instances = []model.Instance{current}

	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 1 || tr.requests[0].method != http.MethodPut || tr.requests[0].path != "/naming/v1/instances" {
		t.Fatalf("expected one protocol update, got %+v", tr.requests)
	}
	var payload []polarisapi.Instance
	if err := json.Unmarshal(tr.requests[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Protocol != "TCP" {
		t.Fatalf("unexpected protocol update payload: %+v", payload)
	}

	current.protocol = "TCP"
	consumer.instances = []model.Instance{current}
	before := len(tr.requests)
	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != before {
		t.Fatalf("converged protocol triggered another write: %+v", tr.requests[before:])
	}
}

func TestCustomProtocolsReconcile(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	svc := testService()
	svc.Spec.Ports[0].Name = "http"
	svc.Annotations[util.PolarisProtocols] = `{"http":"grpc"}`
	current := ownedInstance("10.0.0.1")
	consumer := p.consumer.(*testConsumer)
	consumer.instances = []model.Instance{current}

	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 1 || tr.requests[0].method != http.MethodPut ||
		tr.requests[0].path != "/naming/v1/instances" {
		t.Fatalf("expected custom protocol update, got %+v", tr.requests)
	}
	var payload []polarisapi.Instance
	if err := json.Unmarshal(tr.requests[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Protocol != "grpc" {
		t.Fatalf("custom protocol was not applied: %+v", payload)
	}

	current.protocol = "grpc"
	consumer.instances = []model.Instance{current}
	delete(svc.Annotations, util.PolarisProtocols)
	before := len(tr.requests)
	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests)-before != 1 || tr.requests[len(tr.requests)-1].method != http.MethodPut ||
		tr.requests[len(tr.requests)-1].path != "/naming/v1/instances" {
		t.Fatalf("expected protocol fallback update, got %+v", tr.requests[before:])
	}
	if err := json.Unmarshal(tr.requests[len(tr.requests)-1].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Protocol != "TCP" {
		t.Fatalf("endpoint protocol fallback was not applied: %+v", payload)
	}

	current.protocol = "TCP"
	consumer.instances = []model.Instance{current}
	before = len(tr.requests)
	if err := p.processSyncInstance(svc); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != before {
		t.Fatalf("converged custom protocol triggered another write: %+v", tr.requests[before:])
	}
}

func TestInvalidCustomProtocolsDoNotWriteInstances(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	svc := testService()
	svc.Annotations[util.PolarisProtocols] = `{"unknown":"grpc"}`
	p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}

	err := p.processSyncInstance(svc)
	if err == nil || !strings.Contains(err.Error(), "unknown Service port name") {
		t.Fatalf("invalid custom protocol annotation was accepted: %v", err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("invalid custom protocol annotation caused writes: %+v", tr.requests)
	}
}

func TestPodMetadataConvergesThroughResync(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	svc := testService()
	storeObject(t, stores.services, svc)
	addTestEndpoint(t, stores, "10.0.0.1")
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	p.appliedServiceCache.Store(task.Key(), svc)
	p.resyncServiceCache.Store(task.Key(), svc)
	current := ownedInstance("10.0.0.1")
	current.metadata["app"] = "old"
	current.metadata[util.PolarisOldSource] = Source
	current.metadata[util.PolarisProtocol] = "http"
	consumer := p.consumer.(*testConsumer)
	consumer.instances = []model.Instance{current}
	for _, labels := range []map[string]string{{"app": "new", "region": "", "version": "v2"}, {"version": "v2"}} {
		storeObject(t, stores.pods, &v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod", Labels: labels}})
		before := len(tr.requests)
		p.resyncWorker()
		p.handleTask(p.process)
		updates := 0
		for _, request := range tr.requests[before:] {
			if request.path != "/naming/v1/instances" {
				continue
			}
			updates++
			if request.method != http.MethodPut {
				t.Fatalf("expected update, got %s", request.method)
			}
			var payload []polarisapi.Instance
			if err := json.Unmarshal(request.body, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 1 {
				t.Fatalf("instances=%d", len(payload))
			}
			written := payload[0]
			if written.Protocol != "TCP" || written.Version != "v2" {
				t.Fatalf("protocol/version not propagated: %+v", written)
			}
			if written.Metadata[util.PolarisOldSource] != Source || written.Metadata[util.PolarisProtocol] != "http" {
				t.Fatal("legacy metadata lost")
			}
			if _, exists := written.Metadata["app"]; exists != (labels["app"] != "") {
				t.Fatalf("label removal not propagated: %v", written.Metadata)
			}
			current.metadata = written.Metadata
			current.version = written.Version
			consumer.instances = []model.Instance{current}
		}
		if updates != 1 {
			t.Fatalf("updates=%d want=1", updates)
		}
		before = len(tr.requests)
		p.resyncWorker()
		p.handleTask(p.process)
		for _, request := range tr.requests[before:] {
			if request.path == "/naming/v1/instances" {
				t.Fatal("unchanged metadata triggered another instance update")
			}
		}
	}
}

func TestInvalidMetadataDoesNotWriteInstances(t *testing.T) {
	for _, raw := range []string{`{"tenant":`, `{"tenant":42}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			addTestEndpoint(t, stores, "10.0.0.1")
			instance := ownedInstance("10.0.0.1")
			instance.metadata["tenant"] = "keep"
			p.consumer.(*testConsumer).instances = []model.Instance{instance}
			svc := testService()
			svc.Annotations[util.PolarisMetadata] = raw
			if err := p.processSyncInstance(svc); err == nil {
				t.Fatal("invalid metadata accepted")
			}
			if len(tr.requests) != 0 {
				t.Fatal("invalid metadata caused instance writes")
			}
		})
	}
}

func TestPodLookupFailureDoesNotWriteInstances(t *testing.T) {
	for _, notReady := range []bool{false, true} {
		name := "ready"
		if notReady {
			name = "not ready"
		}
		t.Run(name, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			addTestEndpoint(t, stores, "10.0.0.1", "10.0.0.2", "10.0.0.3")
			storeObject(t, stores.pods, &v1.Pod{ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns", Name: "pod", Labels: map[string]string{"tenant": "new"},
			}})
			ep, err := p.endpointsLister.Endpoints("ns").Get("svc")
			if err != nil {
				t.Fatal(err)
			}
			ep = ep.DeepCopy()
			subset := &ep.Subsets[0]
			subset.Addresses[0].TargetRef.Name = "missing-pod"
			if notReady {
				subset.NotReadyAddresses = subset.Addresses[:1]
				subset.Addresses = subset.Addresses[1:]
			}
			storeObject(t, stores.endpoints, ep)

			missing := ownedInstance("10.0.0.1")
			missing.metadata["tenant"] = "keep"
			existing := ownedInstance("10.0.0.2")
			existing.metadata["tenant"] = "old"
			p.consumer.(*testConsumer).instances = []model.Instance{
				missing, existing, ownedInstance("10.0.0.4"),
			}
			err = p.processSyncInstance(testService())
			if !k8serrors.IsNotFound(err) || !strings.Contains(err.Error(), "ns/missing-pod") {
				t.Errorf("Pod lookup error was not propagated with context: %v", err)
			}
			if len(tr.requests) != 0 {
				t.Errorf("incomplete Pod data caused %d instance writes", len(tr.requests))
			}
		})
	}
}

func TestPodLookupRecoveryConverges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{name: "updated labels", labels: map[string]string{"tenant": "new"}},
		{name: "nil labels"},
		{name: "empty labels", labels: map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			svc := testService()
			storeObject(t, stores.services, svc)
			addTestEndpoint(t, stores, "10.0.0.1")
			pod, err := p.podLister.Pods("ns").Get("pod")
			if err != nil {
				t.Fatal(err)
			}
			if err := stores.pods.Delete(pod); err != nil {
				t.Fatal(err)
			}
			current := ownedInstance("10.0.0.1")
			current.metadata["tenant"] = "keep"
			consumer := p.consumer.(*testConsumer)
			consumer.instances = []model.Instance{current}
			task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
			key := task.QueueKey()
			p.appliedServiceCache.Store(task.Key(), svc)
			p.resyncServiceCache.Store(task.Key(), svc)
			p.insertTask(task)
			p.handleTask(p.process)
			instanceWrites := countRequests(tr.requests, http.MethodPost, "/naming/v1/instances") +
				countRequests(tr.requests, http.MethodPut, "/naming/v1/instances") +
				countRequests(tr.requests, http.MethodPost, "/naming/v1/instances/delete")
			if p.queue.NumRequeues(key) != 1 || instanceWrites != 0 {
				t.Fatalf("missing Pod should retry without writes: retries=%d requests=%d",
					p.queue.NumRequeues(key), instanceWrites)
			}

			pod = pod.DeepCopy()
			pod.Labels = tc.labels
			storeObject(t, stores.pods, pod)
			before := len(tr.requests)
			p.handleTask(p.process)
			if p.queue.NumRequeues(key) != 0 {
				t.Fatal("recovered Pod lookup still retried")
			}
			var instanceRequests []capturedRequest
			for _, request := range tr.requests[before:] {
				if request.path == "/naming/v1/instances" || request.path == "/naming/v1/instances/delete" {
					instanceRequests = append(instanceRequests, request)
				}
			}
			if len(instanceRequests) != 1 || instanceRequests[0].method != http.MethodPut ||
				instanceRequests[0].path != "/naming/v1/instances" {
				t.Fatalf("expected one update after recovery: %v", tr.requests)
			}
			var payload []polarisapi.Instance
			if err := json.Unmarshal(instanceRequests[0].body, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 1 {
				t.Fatalf("instances=%d want=1", len(payload))
			}
			written := payload[0]
			tenant, exists := written.Metadata["tenant"]
			wantTenant, wantExists := tc.labels["tenant"]
			if tenant != wantTenant || exists != wantExists {
				t.Fatalf("recovered labels not propagated: %v", written.Metadata)
			}
			if written.Metadata[util.PolarisSource] != Source || written.Metadata[util.PolarisClusterName] != "test" {
				t.Fatal("instance ownership metadata lost")
			}
			current.metadata = written.Metadata
			consumer.instances = []model.Instance{current}
			before = len(tr.requests)
			p.resyncWorker()
			p.handleTask(p.process)
			for _, request := range tr.requests[before:] {
				if request.path == "/naming/v1/instances" || request.path == "/naming/v1/instances/delete" {
					t.Fatal("metadata did not converge after Pod lookup recovery")
				}
			}
		})
	}
}
