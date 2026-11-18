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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/golang/protobuf/jsonpb"
	"github.com/polarismesh/polaris-go/pkg/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	corelisters "k8s.io/client-go/listers/core/v1"
	k8stesting "k8s.io/client-go/testing"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func pendingTestInstance(host, uid string) polarisapi.Instance {
	return polarisapi.Instance{Namespace: "ns", Service: "svc", Host: host, Port: util.IntPtr(80),
		Metadata: map[string]string{
			util.PolarisSource: Source, util.PolarisClusterName: "test", util.K8sServiceUID: uid,
		}}
}

func mockRemoteInstances(tr *testTransport, remote *[]polarisapi.Instance) {
	handler := tr.respond
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet && request.URL.Path == "/naming/v1/instances" {
			return instanceQueryResponse(*remote), nil
		}
		response, err := successResponse(), error(nil)
		if handler != nil {
			response, err = handler(request)
		}
		if err != nil || response.StatusCode != http.StatusOK ||
			(request.URL.Path != "/naming/v1/instances" && request.URL.Path != "/naming/v1/instances/delete") {
			return response, err
		}
		var payload []polarisapi.Instance
		if err = json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if request.URL.Path == "/naming/v1/instances/delete" {
			remaining := make([]polarisapi.Instance, 0, len(*remote))
			for _, instance := range *remote {
				deleted := false
				for _, item := range payload {
					deleted = deleted || (item.Host == instance.Host && *item.Port == *instance.Port)
				}
				if !deleted {
					remaining = append(remaining, instance)
				}
			}
			*remote = remaining
		} else if request.Method == http.MethodPost {
			*remote = append(*remote, payload...)
		} else if request.Method == http.MethodPut {
			for i, instance := range *remote {
				for _, item := range payload {
					if item.Host == instance.Host && *item.Port == *instance.Port {
						(*remote)[i] = item
					}
				}
			}
		}
		return response, nil
	}
}

func assertPendingRecord(t *testing.T, p *PolarisController, service *v1.Service, want bool) {
	t.Helper()
	key := newServiceSyncState(service, "test", syncStatePendingDelete).key()
	record, found, err := p.syncStateStore.Get(context.Background(), key)
	if err != nil || found != want || (found && record.Phase != syncStatePendingDelete) {
		t.Fatalf("pending record %s=%+v found=%v want=%v error=%v", key, record, found, want, err)
	}
}

func TestPendingCleanupRechecksServiceAfterInstanceQuery(t *testing.T) {
	for _, recreated := range []bool{false, true} {
		t.Run(map[bool]string{false: "same UID reenabled", true: "new UID created"}[recreated], func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			old := testService()
			old.UID = "uid-old"
			inactive := old.DeepCopy()
			inactive.Annotations[util.PolarisSync] = "false"
			storeObject(t, stores.services, inactive)
			addTestEndpoint(t, stores, "10.0.0.1")
			if err := p.markPendingSyncState(old); err != nil {
				t.Fatal(err)
			}
			current := old.DeepCopy()
			if recreated {
				current.UID = "uid-new"
			}
			tr.respond = func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet && request.URL.Path == "/naming/v1/instances" {
					if err := stores.services.Update(current); err != nil {
						return nil, err
					}
					return instanceQueryResponse([]polarisapi.Instance{pendingTestInstance("10.0.0.1", "uid-old")}), nil
				}
				return successResponse(), nil
			}
			if err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()); err != nil {
				t.Fatal(err)
			}
			if len(tr.writes()) != 0 {
				t.Fatalf("current active Service was deleted after discovery: %+v", tr.writes())
			}
			if recreated {
				assertPendingRecord(t, p, old, true)
			} else {
				record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-old")
				if err != nil || !found || record.Phase != syncStateActive {
					t.Fatalf("reenabled Service was not reactivated: %+v found=%v error=%v", record, found, err)
				}
			}
		})
	}
}

func TestPendingCleanupRechecksServiceAfterProtectedStateWrite(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	old := testService()
	old.UID = "uid-old"
	if err := p.markPendingSyncState(old); err != nil {
		t.Fatal(err)
	}
	current := old.DeepCopy()
	current.UID = "uid-new"
	storeObject(t, stores.services, current)
	addTestEndpoint(t, stores, "10.0.0.1")
	remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", "uid-old"), pendingTestInstance("10.0.0.2", "uid-old")}
	mockRemoteInstances(tr, &remote)
	p.client.(*fake.Clientset).PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if err := stores.services.Update(old.DeepCopy()); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
	if err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()); err != nil {
		t.Fatal(err)
	}
	if len(tr.writes()) != 0 || len(remote) != 2 {
		t.Fatalf("Service became active while persisting protection: writes=%+v remote=%+v", tr.writes(), remote)
	}
}

func TestProtectedCleanupSurvivesInterruptedTakeover(t *testing.T) {
	for _, takeoverFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "new Service deleted immediately", true: "update fails before restart"}[takeoverFails],
			func(t *testing.T) {
				p, stores := newTestController(t)
				tr := mockPolarisHTTP(t)
				old := testService()
				old.UID = "uid-old"
				if err := p.markPendingSyncState(old); err != nil {
					t.Fatal(err)
				}
				current := old.DeepCopy()
				current.UID = "uid-new"
				storeObject(t, stores.services, current)
				addTestEndpoint(t, stores, "10.0.0.1")
				storeObject(t, stores.pods, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod", Labels: map[string]string{"app": "test"}},
					Status: v1.PodStatus{Phase: v1.PodRunning, PodIP: "10.0.0.1",
						Conditions: []v1.PodCondition{{Type: v1.PodReady, Status: v1.ConditionTrue}}},
				})
				instance := ownedInstance("10.0.0.1")
				instance.metadata[util.K8sServiceUID] = "uid-old"
				p.consumer.(*testConsumer).instances = []model.Instance{instance}
				remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", "uid-old")}
				tr.respond = func(request *http.Request) (*http.Response, error) {
					if takeoverFails && request.Method == http.MethodPut && request.URL.Path == "/naming/v1/instances" {
						return nil, errors.New("UID takeover unavailable")
					}
					return successResponse(), nil
				}
				mockRemoteInstances(tr, &remote)
				oldTask := newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()
				if err := p.processPendingSyncState(oldTask); err != nil {
					t.Fatal(err)
				}
				assertPendingRecord(t, p, old, true)
				if len(tr.writes()) != 0 {
					t.Fatal("healthy current endpoint was deleted")
				}
				if takeoverFails {
					if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err == nil {
						t.Fatal("expected takeover failure")
					}
					p.syncStateStore = newSyncStateStore(p.client)
					if err := p.recoverSyncStates(context.Background()); err != nil {
						t.Fatal(err)
					}
					assertPendingRecord(t, p, old, true)
				}
				if err := stores.services.Delete(current); err != nil {
					t.Fatal(err)
				}
				if err := p.recoverSyncStates(context.Background()); err != nil {
					t.Fatal(err)
				}
				for p.queue.Len() > 0 {
					task, _ := takeQueueTask(t, p)
					if err := p.process(task); err != nil {
						t.Fatal(err)
					}
				}
				if len(remote) != 0 {
					t.Fatalf("interrupted takeover orphaned instances: %+v", remote)
				}
				assertPendingRecord(t, p, old, false)
			})
	}
}

func TestServiceDeletionCleansLegacyOwnedInstances(t *testing.T) {
	for _, operation := range []string{"deleted", "disabled", "first migration fails then disabled"} {
		t.Run(operation, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			service := testService()
			service.UID = "uid-legacy"
			task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
			legacy := pendingTestInstance("10.0.0.1", "")
			delete(legacy.Metadata, util.K8sServiceUID)
			delete(legacy.Metadata, util.PolarisSource)
			legacy.Metadata[util.PolarisOldSource] = Source
			remote := []polarisapi.Instance{legacy}
			if operation == "first migration fails then disabled" {
				storeObject(t, stores.services, service)
				addTestEndpoint(t, stores, "10.0.0.1")
				p.consumer.(*testConsumer).err = errors.New("first migration discovery unavailable")
				if err := p.syncService(task); err == nil {
					t.Fatal("expected initial migration failure")
				}
			} else {
				p.appliedServiceCache.Store(task.Key(), service.DeepCopy())
				if err := p.ensureActiveSyncState(service); err != nil {
					t.Fatal(err)
				}
			}
			if operation != "deleted" {
				disabled := service.DeepCopy()
				disabled.Annotations[util.PolarisSync] = "false"
				storeObject(t, stores.services, disabled)
			}
			foreign := pendingTestInstance("10.0.0.2", "")
			foreign.Metadata[util.PolarisSource] = "sdk"
			agent := pendingTestInstance("10.0.0.3", "")
			agent.Metadata[util.PolarisSource] = "javaagent"
			otherCluster := pendingTestInstance("10.0.0.4", "")
			otherCluster.Metadata[util.PolarisClusterName] = "other"
			newUID := pendingTestInstance("10.0.0.5", "uid-new")
			remote = append(remote, foreign, agent, otherCluster, newUID)
			mockRemoteInstances(tr, &remote)
			if err := p.syncService(task); err != nil {
				t.Fatal(err)
			}
			for p.queue.Len() > 0 {
				cleanup, _ := takeQueueTask(t, p)
				if err := p.process(cleanup); err != nil {
					t.Fatal(err)
				}
			}
			if len(remote) != 4 || remote[0].Host != foreign.Host {
				t.Fatalf("legacy cleanup violated ownership or missed instance: %+v", remote)
			}
			assertPendingRecord(t, p, service, false)
		})
	}
}

func TestPendingCleanupAcceptsHeartbeatInstancesWithoutDeletingThem(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = "uid-heartbeat"
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}

	remote := []*apiservice.Instance{
		{
			Id: wrapperspb.String("controller"), Namespace: wrapperspb.String("ns"),
			Service: wrapperspb.String("svc"), Host: wrapperspb.String("10.0.0.1"),
			Port: wrapperspb.UInt32(80),
			Metadata: map[string]string{
				util.PolarisSource:      Source,
				util.PolarisClusterName: "test",
				util.K8sServiceUID:      string(service.UID),
			},
		},
		{
			Id: wrapperspb.String("sdk-heartbeat"), Namespace: wrapperspb.String("ns"),
			Service: wrapperspb.String("svc"), Host: wrapperspb.String("10.0.0.2"),
			Port: wrapperspb.UInt32(9090), EnableHealthCheck: wrapperspb.Bool(true),
			HealthCheck: &apiservice.HealthCheck{
				Type:      apiservice.HealthCheck_HEARTBEAT,
				Heartbeat: &apiservice.HeartbeatHealthCheck{Ttl: wrapperspb.UInt32(10)},
			},
		},
	}
	tr.respond = func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/naming/v1/instances":
			amount := wrapperspb.UInt32(uint32(len(remote)))
			body, err := (&jsonpb.Marshaler{EmitDefaults: true}).MarshalToString(&apiservice.BatchQueryResponse{
				Code: wrapperspb.UInt32(polarisapi.ExecuteSuccess), Amount: amount, Size: amount, Instances: remote,
			})
			if err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)),
			}, nil
		case request.Method == http.MethodPost && request.URL.Path == "/naming/v1/instances/delete":
			var payload []polarisapi.Instance
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			remaining := remote[:0]
			for _, instance := range remote {
				deleted := false
				for _, candidate := range payload {
					if candidate.Host == instance.GetHost().GetValue() &&
						candidate.Port != nil && uint32(*candidate.Port) == instance.GetPort().GetValue() {
						deleted = true
						break
					}
				}
				if !deleted {
					remaining = append(remaining, instance)
				}
			}
			remote = remaining
			return successResponse(), nil
		default:
			return successResponse(), nil
		}
	}

	if err := p.processPendingSyncState(
		newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()); err != nil {
		t.Fatalf("pending cleanup rejected heartbeat response: %v", err)
	}
	writes := tr.writes()
	if len(writes) != 1 || writes[0].path != "/naming/v1/instances/delete" {
		t.Fatalf("unexpected cleanup requests: %+v", tr.requests)
	}
	var deleted []polarisapi.Instance
	if err := json.Unmarshal(writes[0].body, &deleted); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].Host != "10.0.0.1" {
		t.Fatalf("heartbeat instance was not protected: %+v", deleted)
	}
	if len(remote) != 1 || remote[0].GetHost().GetValue() != "10.0.0.2" {
		t.Fatalf("SDK heartbeat instance was deleted or controller instance remained: %+v", remote)
	}
	assertPendingRecord(t, p, service, false)
}

func TestServiceDeletionIgnoresStaleEmptySDKSnapshot(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = "uid-cache"
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	var remote []polarisapi.Instance
	mockRemoteInstances(tr, &remote)
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	consumer := p.consumer.(*testConsumer)
	if len(remote) != 1 || len(consumer.instances) != 0 {
		t.Fatal("fixture requires a registered remote instance and a stale empty SDK cache")
	}
	calls := consumer.calls
	if err := stores.services.Delete(service); err != nil {
		t.Fatal(err)
	}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	if consumer.calls != calls || len(remote) != 0 {
		t.Fatalf("deletion used SDK cache or left remote instances: SDK calls=%d remote=%+v", consumer.calls, remote)
	}
	assertPendingRecord(t, p, service, false)
}

func TestPendingCleanupRetainsStateUntilRemoteDeletionConfirmed(t *testing.T) {
	for _, queryFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete acknowledged but instance remains", true: "verification fails"}[queryFails],
			func(t *testing.T) {
				p, _ := newTestController(t)
				tr := mockPolarisHTTP(t)
				service := testService()
				service.UID = "uid-confirm"
				if err := p.markPendingSyncState(service); err != nil {
					t.Fatal(err)
				}
				queries := 0
				tr.respond = func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet && request.URL.Path == "/naming/v1/instances" {
						queries++
						if queries > 1 && queryFails {
							return nil, errors.New("verification unavailable")
						}
						return instanceQueryResponse([]polarisapi.Instance{pendingTestInstance("10.0.0.1", "uid-confirm")}), nil
					}
					return successResponse(), nil
				}
				if err := p.processPendingSyncState(newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()); err == nil {
					t.Fatal("unconfirmed deletion returned success")
				}
				assertPendingRecord(t, p, service, true)
				p.syncStateStore = newSyncStateStore(p.client)
				if err := p.recoverSyncStates(context.Background()); err != nil {
					t.Fatal(err)
				}
				if p.queue.Len() != 1 {
					t.Fatal("unconfirmed cleanup could not recover after restart")
				}
			})
	}
}

type cleanupPodLister struct {
	corelisters.PodLister
	onGet func()
}

func (l cleanupPodLister) Pods(namespace string) corelisters.PodNamespaceLister {
	return cleanupPodNamespaceLister{PodNamespaceLister: l.PodLister.Pods(namespace), onGet: l.onGet}
}

type cleanupPodNamespaceLister struct {
	corelisters.PodNamespaceLister
	onGet func()
}

func (l cleanupPodNamespaceLister) Get(name string) (*v1.Pod, error) {
	l.onGet()
	return l.PodNamespaceLister.Get(name)
}

func TestPendingCleanupRechecksEndpointsAfterPodRead(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	old := testService()
	old.UID = "uid-old"
	if err := p.markPendingSyncState(old); err != nil {
		t.Fatal(err)
	}
	current := old.DeepCopy()
	current.UID = "uid-new"
	storeObject(t, stores.services, current)
	addTestEndpoint(t, stores, "10.0.0.1")
	remote := []polarisapi.Instance{pendingTestInstance("10.0.0.2", "uid-old")}
	mockRemoteInstances(tr, &remote)
	changed := false
	p.podLister = cleanupPodLister{PodLister: p.podLister, onGet: func() {
		if changed {
			return
		}
		changed = true
		addTestEndpoint(t, stores, "10.0.0.1", "10.0.0.2")
	}}
	if err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()); err != nil {
		t.Fatal(err)
	}
	if len(tr.writes()) != 0 || len(remote) != 1 {
		t.Fatalf("new desired endpoint was deleted after Pod reads: writes=%+v remote=%+v", tr.writes(), remote)
	}
	assertPendingRecord(t, p, old, true)
}

func TestPendingCleanupKeepsStateWhenNamespaceOrPodSnapshotIsIncomplete(t *testing.T) {
	for _, missing := range []string{"Namespace", "Pod", "Endpoints"} {
		t.Run(missing, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			old := testService()
			old.UID = "uid-old"
			if err := p.markPendingSyncState(old); err != nil {
				t.Fatal(err)
			}
			current := old.DeepCopy()
			current.UID = "uid-new"
			if missing == "Namespace" {
				current.Annotations = nil
			}
			storeObject(t, stores.services, current)
			addTestEndpoint(t, stores, "10.0.0.1")
			if missing == "Pod" {
				if err := stores.pods.Delete(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod"}}); err != nil {
					t.Fatal(err)
				}
			} else if missing == "Endpoints" {
				if err := stores.endpoints.Delete(&v1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"}}); err != nil {
					t.Fatal(err)
				}
			}
			remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", "uid-old")}
			mockRemoteInstances(tr, &remote)
			if err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()); err == nil {
				t.Fatal("incomplete cleanup snapshot returned success")
			}
			if len(tr.writes()) != 0 || len(remote) != 1 {
				t.Fatal("incomplete cleanup snapshot triggered a deletion")
			}
			assertPendingRecord(t, p, old, true)
		})
	}
}

func TestPendingCleanupProtectsReadyAndNotReadyLegacyEndpoints(t *testing.T) {
	for _, notReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "Ready", true: "NotReady"}[notReady], func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			old := testService()
			old.UID = "uid-old"
			if err := p.markPendingSyncState(old); err != nil {
				t.Fatal(err)
			}
			current := old.DeepCopy()
			current.UID = "uid-new"
			storeObject(t, stores.services, current)
			addTestEndpoint(t, stores, "10.0.0.1")
			if notReady {
				object, _, _ := stores.endpoints.GetByKey("ns/svc")
				endpoints := object.(*v1.Endpoints).DeepCopy()
				endpoints.Subsets[0].NotReadyAddresses = endpoints.Subsets[0].Addresses
				endpoints.Subsets[0].Addresses = nil
				storeObject(t, stores.endpoints, endpoints)
			}
			remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", "")}
			delete(remote[0].Metadata, util.K8sServiceUID)
			mockRemoteInstances(tr, &remote)
			if err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask()); err != nil {
				t.Fatal(err)
			}
			if len(tr.writes()) != 0 || len(remote) != 1 {
				t.Fatal("current desired legacy endpoint was deleted")
			}
			assertPendingRecord(t, p, old, true)
		})
	}
}
