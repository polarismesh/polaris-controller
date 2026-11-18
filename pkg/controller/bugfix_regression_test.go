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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

func TestAuthenticatedAliasTakeoverAfterOwnerRewrite(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	tr.respond = func(r *http.Request) (*http.Response, error) {
		response, err := state.responder(r)
		if r.Method == http.MethodPost && r.URL.Path == "/naming/v1/service/alias" && err == nil {
			for key, alias := range state.aliases {
				alias.Owners = "authenticated-account-id"
				state.aliases[key] = alias
			}
		}
		return response, err
	}
	first := aliasTestService("source-first", "uid-first", time.Unix(100, 0))
	next := aliasTestService("source-next", "uid-next", time.Unix(200, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, next)
	if err := p.reconcileServiceAlias(first); err != nil {
		t.Fatal(err)
	}
	if err := stores.services.Delete(first); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.reconcileServiceAlias(next); err != nil {
			t.Fatal(err)
		}
	}
	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != next.Name {
		t.Fatalf("controller-created alias not taken over: target=%s owners=%s want=%s", actual.Service, actual.Owners, next.Name)
	}
}

func TestPendingCleanupAcceptsSDKZeroPort(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	old := testService()
	old.UID = "old-uid"
	remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", string(old.UID)), {
		Namespace: "ns", Service: "svc", Host: "10.0.0.2", Port: util.IntPtr(0), Metadata: map[string]string{util.PolarisSource: "sdk"},
	}}
	mockRemoteInstances(tr, &remote)
	if err := p.markPendingSyncState(old); err != nil {
		t.Fatal(err)
	}
	err := p.processPendingSyncState(newServiceSyncState(old, "test", syncStatePendingDelete).sourceTask())
	if err != nil || len(remote) != 1 || remote[0].Host != "10.0.0.2" {
		t.Fatalf("cleanup blocked by valid SDK: error=%v remaining=%d writes=%d", err, len(remote), len(tr.writes()))
	}
}

func TestLastErrorIsBoundedWithoutBlockingShard(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = "long-error"
	record, err := p.syncStateStore.Upsert(context.Background(), newServiceSyncState(service, "test", syncStatePendingDelete))
	if err != nil {
		t.Fatal(err)
	}
	p.recordSyncStateFailure(record, errors.New(strings.Repeat("中文故障", syncStateMaxSize/6)))
	current, _, err := p.syncStateStore.Get(context.Background(), record.key())
	if err != nil {
		t.Fatal(err)
	}
	if current.Attempts != 1 || len(current.LastError) == 0 || len(current.LastError) > 4096 || !utf8.ValidString(current.LastError) || !strings.HasSuffix(current.LastError, "[truncated]") {
		t.Fatalf("failure summary not persisted with bounded length: attempts=%d lastErrorBytes=%d", current.Attempts, len(current.LastError))
	}
	other := service.DeepCopy()
	for i := 0; i < 1000; i++ {
		other.UID = types.UID(fmt.Sprintf("same-shard-%d", i))
		otherRecord := newServiceSyncState(other, "test", syncStatePendingDelete)
		if syncStateShard(otherRecord.key()) != syncStateShard(record.key()) {
			continue
		}
		if _, err := p.syncStateStore.Upsert(context.Background(), otherRecord); err != nil {
			t.Fatalf("bounded failure blocked another record in the same shard: %v", err)
		}
		if _, found, err := p.syncStateStore.Get(context.Background(), otherRecord.key()); err != nil || !found {
			t.Fatalf("second record was not persisted: found=%v error=%v", found, err)
		}
		return
	}
	t.Fatal("fixture did not find another record in the same shard")
}

func TestRepeatedDeletionPreservesAttemptsAndLabels(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(*http.Request) (*http.Response, error) { return nil, errors.New("review read failure") }
	service := testService()
	service.UID = "retry-state"
	service.Labels = map[string]string{"env": "v1"}
	if err := p.ensureActiveSyncState(service); err != nil {
		t.Fatal(err)
	}
	if err := p.persistAppliedServiceLabels(service, service.Labels); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = p.deleteServiceWithState(service)
	}
	current, _, err := p.syncStateStore.Get(context.Background(), newServiceSyncState(service, "test", syncStatePendingDelete).key())
	if err != nil {
		t.Fatal(err)
	}
	if current.Attempts != 3 || current.AppliedLabels["env"] != "v1" {
		t.Fatalf("delete retry reset persisted state: attempts=%d labels=%v wantAttempts=3", current.Attempts, current.AppliedLabels)
	}
}

func TestUnknownSyncStateWriteOutcomeImmediateDeletion(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	mockPolarisInstances(tr, p.consumer.(*testConsumer))
	client := p.client.(*fake.Clientset)
	service := testService()
	service.UID = "unknown-write"
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	committed := false
	client.PrependReactor("update", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if committed {
			return false, nil, nil
		}
		cm := action.(k8stesting.UpdateAction).GetObject().(*v1.ConfigMap)
		if cm.Data[syncStateDataKey] == "{}" {
			return false, nil, nil
		}
		if err := client.Tracker().Update(v1.SchemeGroupVersion.WithResource("configmaps"), cm.DeepCopy(), cm.Namespace); err != nil {
			t.Fatal(err)
		}
		committed = true
		return true, nil, errors.New("review response lost after commit")
	})
	if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("fixture did not persist state")
	}
	if len(tr.writes()) == 0 {
		t.Fatal("fixture did not reach the Polaris instance write")
	}
	if err := stores.services.Delete(service); err != nil {
		t.Fatal(err)
	}
	if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err != nil {
		t.Fatal(err)
	}
	taskKey := (&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}).Key()
	if _, cached := p.appliedServiceCache.Load(taskKey); cached {
		t.Fatal("immediate deletion did not release the applied Service cache")
	}
	durableStore := newSyncStateStore(client)
	_, found, err := durableStore.Get(context.Background(), "test|unknown-write")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("immediate deletion did not release the committed sync state")
	}
}

type vpcTestInstance struct {
	testInstance
	vpc string
}

func (i vpcTestInstance) GetVpcId() string { return i.vpc }

func TestOtherVPCDoesNotSuppressDefaultVPCTarget(t *testing.T) {
	p, _ := newTestController(t)
	instances := []model.Instance{vpcTestInstance{testInstance: testInstance{host: "10.0.0.1", metadata: map[string]string{util.PolarisSource: "sdk"}}, vpc: "vpc-a"}}
	owned, all := address.GetAddressMapFromPolarisInstance(instances, "test")
	desired := address.InstanceSet{"10.0.0.1-80": &address.Address{IP: "10.0.0.1", Port: 80}}
	add, _, _ := p.CompareInstance(testService(), desiredHealthCheck{}, desired, owned, all)
	if len(add) != 1 {
		t.Fatalf("instance in vpc-a incorrectly blocks controller target with empty VPC: adds=%d", len(add))
	}
}

func TestInitialMetadataReadFailureKeepsCreationBaseline(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	remote := map[string]string{}
	created, failedRead := false, false
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/services" {
			switch r.Method {
			case http.MethodPost:
				if created {
					return aliasJSONResponse(http.StatusBadRequest, `{"code":400201,"info":"exists"}`)
				}
				if !created {
					var request []polarisapi.CreateServiceRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatal(err)
					}
					remote = request[0].Metadata
					created = true
				}
			case http.MethodGet:
				if !failedRead {
					failedRead = true
					return nil, errors.New("review first metadata read outage")
				}
				return serviceMetadataResponse(t, remote), nil
			case http.MethodPut:
				var request []polarisapi.Service
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				remote = request[0].Metadata
			}
		}
		return successResponse(), nil
	}
	service := testService()
	service.UID = "first-read-outage"
	service.Labels = map[string]string{"env": "v1", "retired": "yes"}
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	if err := p.syncService(task); err == nil || !created {
		t.Fatal("fixture did not create service before first-read outage")
	}
	changed := service.DeepCopy()
	changed.Labels = map[string]string{"env": "v2"}
	storeObject(t, stores.services, changed)
	for i := 0; i < 3; i++ {
		if err := p.syncService(task); err != nil {
			t.Fatal(err)
		}
	}
	if _, exists := remote["retired"]; exists {
		t.Fatalf("creation baseline lost before initial GET: remote=%v want=map[env:v2]", remote)
	}
}

func TestFailedDeletionReactivationPreservesLabelBaseline(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	old := testService()
	old.UID = "reenable-labels"
	old.Labels = map[string]string{"env": "v1", "retired": "yes"}
	if err := p.ensureActiveSyncState(old); err != nil {
		t.Fatal(err)
	}
	if err := p.persistAppliedServiceLabels(old, old.Labels); err != nil {
		t.Fatal(err)
	}
	tr.respond = func(*http.Request) (*http.Response, error) { return nil, errors.New("review deletion query outage") }
	if err := p.deleteServiceWithState(old); err == nil {
		t.Fatal("fixture did not fail deletion")
	}
	current := old.DeepCopy()
	current.Labels = map[string]string{"env": "v2"}
	storeObject(t, stores.services, current)
	addTestEndpoint(t, stores, "10.0.0.1")
	remote := map[string]string{"env": "v1", "retired": "yes"}
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/services" && r.Method == http.MethodPost {
			return aliasJSONResponse(http.StatusBadRequest, `{"code":400201,"info":"exists"}`)
		}
		if r.URL.Path == "/naming/v1/services" && r.Method == http.MethodGet {
			return serviceMetadataResponse(t, remote), nil
		}
		if r.URL.Path == "/naming/v1/services" && r.Method == http.MethodPut {
			var request []polarisapi.Service
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			remote = request[0].Metadata
		}
		return successResponse(), nil
	}
	if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err != nil {
		t.Fatal(err)
	}
	if _, exists := remote["retired"]; exists {
		t.Fatalf("failed deletion wiped last-applied labels before restart/reactivation: remote=%v", remote)
	}
}

func TestNamespaceAliasWinnerChangeEnqueuesSuccessor(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/service/alias" || r.URL.Path == "/naming/v1/service/aliases" {
			return state.responder(r)
		}
		return successResponse(), nil
	}
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: "true"}}}
	storeObject(t, stores.namespaces, ns)
	first := aliasTestService("source-first", "uid-first", time.Unix(100, 0))
	delete(first.Annotations, util.PolarisSync)
	next := aliasTestService("source-next", "uid-next", time.Unix(200, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, next)
	storeObject(t, stores.endpoints, &v1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: next.Namespace, Name: next.Name}})
	if err := p.reconcileServiceAlias(first); err != nil {
		t.Fatal(err)
	}
	firstTask := &Task{Namespace: "ns", Name: first.Name, ObjectType: KubernetesService}
	p.appliedServiceCache.Store(firstTask.Key(), first)
	remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", string(first.UID))}
	remote[0].Service = first.Name
	mockRemoteInstances(tr, &remote)
	disabled := ns.DeepCopy()
	disabled.Annotations[util.PolarisSync] = "false"
	storeObject(t, stores.namespaces, disabled)
	p.onNamespaceUpdate(ns, disabled)
	queuedNext := false
	for p.queue.Len() > 0 {
		task, _ := takeQueueTask(t, p)
		if task.Name == next.Name {
			queuedNext = true
		}
		if err := p.process(task); err != nil {
			t.Fatal(err)
		}
	}
	actual := state.aliases[aliasTestKey("Production", "order")]
	if !queuedNext || actual.Service != next.Name {
		t.Fatalf("Namespace disable did not hand over alias: successorQueued=%v target=%s removedInstances=%d", queuedNext, actual.Service, len(remote))
	}
}

func TestAliasProvenanceSurvivesRestartWithoutAdoptingExternalAliases(t *testing.T) {
	for _, scenario := range []string{"restart", "same account manual", "other cluster", "recreated", "retargeted", "wrong alias", "malformed marker"} {
		t.Run(scenario, func(t *testing.T) {
			creator, _ := newTestController(t)
			tr := mockPolarisHTTP(t)
			state := newAliasAPITestState()
			tr.respond = state.responder
			old := aliasTestService("old", "old-uid", time.Unix(100, 0))
			if err := creator.reconcileServiceAlias(old); err != nil {
				t.Fatal(err)
			}
			key := aliasTestKey("Production", "order")
			actual := state.aliases[key]
			actual.Owners = "authenticated-account-id"
			switch scenario {
			case "same account manual", "recreated":
				actual.Comment = "manually created"
			case "other cluster":
				other, _ := newTestController(t)
				other.config.PolarisController.ClusterName = "other"
				actual.Comment = other.aliasComment(old)
			case "retargeted":
				actual.Service = "manual-target"
			case "wrong alias":
				var provenance aliasProvenance
				if err := json.Unmarshal([]byte(strings.TrimPrefix(actual.Comment, aliasProvenancePrefix)), &provenance); err != nil {
					t.Fatal(err)
				}
				provenance.Alias = "other"
				encoded, err := json.Marshal(provenance)
				if err != nil {
					t.Fatal(err)
				}
				actual.Comment = aliasProvenancePrefix + string(encoded)
			case "malformed marker":
				actual.Comment = aliasProvenancePrefix + "{"
			}
			state.aliases[key] = actual
			restarted, stores := newTestController(t)
			next := aliasTestService("next", "next-uid", time.Unix(200, 0))
			storeObject(t, stores.services, next)
			if err := restarted.reconcileServiceAlias(next); err != nil {
				t.Fatal(err)
			}
			after := state.aliases[key]
			if scenario == "restart" {
				if after.Service != next.Name || after.Owners != "authenticated-account-id" {
					t.Fatalf("restart did not safely rebind: %+v", after)
				}
			} else if after != actual {
				t.Fatalf("external alias was adopted: before=%+v after=%+v", actual, after)
			}
		})
	}
}

func TestMixedSuccessfulDeleteAndConflictRequiresReadback(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "pending"}[pending], func(t *testing.T) {
			p, _ := newTestController(t)
			tr := mockPolarisHTTP(t)
			service := testService()
			service.UID = "mixed-delete-conflict"
			remote := []polarisapi.Instance{
				pendingTestInstance("10.0.0.1", string(service.UID)),
				pendingTestInstance("10.0.0.2", string(service.UID)),
			}
			reads := 0
			tr.respond = func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					reads++
					return instanceQueryResponse(remote), nil
				}
				remote = nil
				return aliasJSONResponse(http.StatusBadRequest,
					`{"code":400000,"info":"bad request","responses":[`+
						`{"code":200000,"instance":{"host":"10.0.0.1","port":80}},`+
						`{"code":400208,"info":"the same instance request","instance":{"host":"10.0.0.2","port":80}}]}`)
			}

			var err error
			if pending {
				if err = p.markPendingSyncState(service); err != nil {
					t.Fatal(err)
				}
				err = p.processPendingSyncState(
					newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask())
			} else {
				err = p.deleteInstances(service, []address.Address{
					{IP: "10.0.0.1", Port: 80}, {IP: "10.0.0.2", Port: 80},
				})
			}
			wantReads := 1
			if pending {
				wantReads = 2
			}
			if err != nil {
				t.Fatalf("completed success+400208 batch was not confirmed by readback: %v", err)
			}
			if reads != wantReads {
				t.Fatalf("readback calls=%d want=%d", reads, wantReads)
			}
			if pending {
				assertPendingRecord(t, p, service, false)
			}
		})
	}
}

func TestPendingDeleteConflictRequiresReadback(t *testing.T) {
	for _, body := range []string{
		`{"code":400208,"info":"the same instance request"}`,
		`{"code":200000,"responses":[{"code":400208,"instance":{"host":"10.0.0.1","port":80}}]}`,
	} {
		for _, completed := range []bool{false, true} {
			t.Run(body+"/"+map[bool]string{false: "still present", true: "completed"}[completed], func(t *testing.T) {
				p, _ := newTestController(t)
				tr := mockPolarisHTTP(t)
				service := testService()
				service.UID = "delete-conflict"
				remote := []polarisapi.Instance{pendingTestInstance("10.0.0.1", string(service.UID))}
				reads := 0
				tr.respond = func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						reads++
						return instanceQueryResponse(remote), nil
					}
					if completed {
						remote = nil
					}
					return aliasJSONResponse(http.StatusOK, body)
				}
				if err := p.markPendingSyncState(service); err != nil {
					t.Fatal(err)
				}
				err := p.processPendingSyncState(newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask())
				if reads != 2 {
					t.Fatalf("delete conflict did not get readback: reads=%d error=%v", reads, err)
				}
				if completed && err != nil || !completed && !errors.Is(err, polarisapi.ErrDeleteConflict) {
					t.Fatalf("wrong completion result: completed=%v error=%v", completed, err)
				}
				assertPendingRecord(t, p, service, !completed)
			})
		}
	}
}

func TestOrdinaryDeleteConflictRequiresVPCReadback(t *testing.T) {
	for _, response := range []struct {
		name string
		body string
	}{
		{"top-level", `{"code":400208,"info":"the same instance request"}`},
		{"per-instance", `{"code":200000,"responses":[{"code":400208,"instance":{"host":"10.0.0.1","port":80}}]}`},
	} {
		for _, outcome := range []string{"absent-with-other-vpc", "present-in-target-vpc", "readback-denied", "readback-incomplete"} {
			t.Run(response.name+"/"+outcome, func(t *testing.T) {
				p, _ := newTestController(t)
				tr := mockPolarisHTTP(t)
				target := address.Address{IP: "10.0.0.1", Port: 80, VpcID: "vpc-b"}
				remote := []polarisapi.Instance{{
					Namespace: "ns", Service: "svc", Host: target.IP, Port: util.IntPtr(target.Port), VpcID: "vpc-a",
				}}
				if outcome == "present-in-target-vpc" {
					remaining := remote[0]
					remaining.VpcID = target.VpcID
					remote = append(remote, remaining)
				}
				reads := 0
				tr.respond = func(r *http.Request) (*http.Response, error) {
					if r.Method != http.MethodGet {
						return aliasJSONResponse(http.StatusOK, response.body)
					}
					reads++
					if r.URL.Path != "/naming/v1/instances" || r.URL.Query().Get("namespace") != "ns" ||
						r.URL.Query().Get("service") != "svc" {
						t.Fatalf("readback queried a different Service: %s", r.URL)
					}
					switch outcome {
					case "readback-denied":
						return aliasJSONResponse(http.StatusForbidden, `{"code":401000,"info":"unauthorized"}`)
					case "readback-incomplete":
						return aliasJSONResponse(http.StatusOK, `{"code":200000,"amount":1,"size":0,"instances":[]}`)
					default:
						return instanceQueryResponse(remote), nil
					}
				}
				err := p.deleteInstances(testService(), []address.Address{target})
				if reads != 1 {
					t.Fatalf("conflicting delete did not read back exactly once: reads=%d error=%v", reads, err)
				}
				switch outcome {
				case "absent-with-other-vpc":
					if err != nil {
						t.Fatalf("same host/port in another VPC blocked confirmed deletion: %v", err)
					}
				case "present-in-target-vpc":
					if !errors.Is(err, polarisapi.ErrDeleteConflict) {
						t.Fatalf("remaining target was treated as deleted: %v", err)
					}
				case "readback-denied":
					var apiErr *polarisapi.APIError
					if !errors.As(err, &apiErr) || apiErr.Code != 401000 {
						t.Fatalf("readback authorization failure was lost: %v", err)
					}
				case "readback-incomplete":
					if err == nil {
						t.Fatal("incomplete query was treated as successful deletion")
					}
				}
				writes := tr.writes()
				if len(writes) != 1 || writes[0].path != "/naming/v1/instances/delete" {
					t.Fatalf("readback produced unexpected writes: %+v", writes)
				}
				var payload []polarisapi.Instance
				if err := json.Unmarshal(writes[0].body, &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) != 1 || payload[0].VpcID != target.VpcID {
					t.Fatalf("deletion did not preserve target VPC: %+v", payload)
				}
			})
		}
	}
	t.Run("mixed-failure-does-not-confirm-success", func(t *testing.T) {
		p, _ := newTestController(t)
		tr := mockPolarisHTTP(t)
		reads := 0
		tr.respond = func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				reads++
				return instanceQueryResponse(nil), nil
			}
			return aliasJSONResponse(http.StatusOK,
				`{"code":200000,"responses":[{"code":400208},{"code":401000,"info":"unauthorized"}]}`)
		}
		err := p.deleteInstances(testService(), []address.Address{{IP: "10.0.0.1", Port: 80, VpcID: "vpc-b"}})
		if err == nil || errors.Is(err, polarisapi.ErrDeleteConflict) || !strings.Contains(err.Error(), "401000") || reads != 0 {
			t.Fatalf("readback hid a mixed real failure: reads=%d error=%v", reads, err)
		}
	})
}

func TestVPCIdentityPreservesForeignInstancesAndWriteTargets(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		p, _ := newTestController(t)
		tr := mockPolarisHTTP(t)
		sdk := vpcTestInstance{testInstance: testInstance{host: "10.0.0.1", metadata: map[string]string{util.PolarisSource: "sdk"}}, vpc: "vpc-a"}
		own := ownedInstance("10.0.0.1")
		instances := []model.Instance{sdk, own}
		if reversed {
			instances[0], instances[1] = instances[1], instances[0]
		}
		managed, all := address.GetAddressMapFromPolarisInstance(instances, "test")
		desired := address.InstanceSet{"10.0.0.1-80": {IP: "10.0.0.1", Port: 80, Healthy: true, Weight: util.DefaultWeight, Protocol: "TCP"}}
		add, deleted, _ := p.CompareInstance(testService(), desiredHealthCheck{}, desired, managed, all)
		if len(add) != 0 || len(deleted) != 0 || len(all) != 2 {
			t.Fatalf("VPC identity collapsed: add=%v deleted=%v all=%v", add, deleted, all)
		}
		target := address.Address{IP: "10.0.0.1", Port: 80, VpcID: "vpc-b"}
		if err := p.deleteInstances(testService(), []address.Address{target}); err != nil {
			t.Fatal(err)
		}
		var written []polarisapi.Instance
		if err := json.Unmarshal(tr.writes()[0].body, &written); err != nil {
			t.Fatal(err)
		}
		if len(written) != 1 || written[0].VpcID != "vpc-b" {
			t.Fatalf("VPC was lost in deletion: %+v", written)
		}
	}
}

type shutdownConsumer struct {
	api.ConsumerAPI
	destroyed chan struct{}
}

func (c *shutdownConsumer) Destroy() { close(c.destroyed) }

type shutdownProvider struct {
	api.ProviderAPI
	destroyed chan struct{}
}

func (p *shutdownProvider) Destroy() { close(p.destroyed) }

func TestRunJoinsWorkerAndUnregistersBeforeSDKDestroy(t *testing.T) {
	p, _ := newTestController(t)
	provider := &shutdownProvider{destroyed: make(chan struct{})}
	consumer := &shutdownConsumer{destroyed: make(chan struct{})}
	p.provider, p.consumer = provider, consumer
	p.podsSynced = func() bool { return true }
	p.servicesSynced, p.endpointsSynced, p.namespaceSynced, p.configMapSynced = p.podsSynced, p.podsSynced, p.podsSynced, p.podsSynced
	p.config.PolarisController.ResyncDuration = time.Hour
	p.config.PolarisController.HealthCheckDuration = time.Hour
	tr := mockPolarisHTTP(t)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/namespaces" {
			close(entered)
			<-r.Context().Done()
			close(cancelled)
			<-release
			return nil, r.Context().Err()
		}
		return successResponse(), nil
	}
	stop, done, unregistered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p.insertTask(&Task{Namespace: "shutdown", Name: "shutdown", ObjectType: KubernetesNamespace})
	go func() {
		p.RunWithShutdown(1, stop, func() {
			select {
			case <-provider.destroyed:
				t.Error("provider destroyed before deregistration")
			default:
			}
			close(unregistered)
		})
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	close(stop)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel request")
	}
	select {
	case <-done:
		t.Fatal("Run returned before worker completed")
	case <-provider.destroyed:
		t.Fatal("SDK destroyed while worker running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not join worker")
	}
	for _, ch := range []<-chan struct{}{unregistered, provider.destroyed, consumer.destroyed} {
		select {
		case <-ch:
		default:
			t.Fatal("shutdown stage incomplete")
		}
	}
}

func TestRunCacheSyncAbortUnregistersAndDestroysOnce(t *testing.T) {
	p, _ := newTestController(t)
	provider := &shutdownProvider{destroyed: make(chan struct{})}
	consumer := &shutdownConsumer{destroyed: make(chan struct{})}
	p.provider, p.consumer = provider, consumer
	p.podsSynced = func() bool { return false }
	p.servicesSynced, p.endpointsSynced, p.namespaceSynced, p.configMapSynced = p.podsSynced, p.podsSynced, p.podsSynced, p.podsSynced
	stop := make(chan struct{})
	close(stop)
	called := 0
	p.RunWithShutdown(1, stop, func() {
		called++
		select {
		case <-provider.destroyed:
			t.Fatal("provider destroyed before unregister on cache-sync abort")
		default:
		}
	})
	if called != 1 {
		t.Fatalf("unregister count=%d", called)
	}
	select {
	case <-provider.destroyed:
	default:
		t.Fatal("provider not destroyed")
	}
	select {
	case <-consumer.destroyed:
	default:
		t.Fatal("consumer not destroyed")
	}
}

func TestUnknownWriteBeforeCommitDoesNotAdoptOtherUID(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = types.UID("not-committed")
	storeObject(t, stores.services, service)
	client := p.client.(*fake.Clientset)
	client.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("failed before commit")
	})
	if err := p.ensureActiveSyncState(service); err == nil {
		t.Fatal("fixture did not fail before commit")
	}
	client.ReactionChain = client.ReactionChain[1:]
	if err := stores.services.Delete(service); err != nil {
		t.Fatal(err)
	}
	if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err != nil {
		t.Fatal(err)
	}
	if p.queue.Len() != 0 || len(tr.writes()) != 0 {
		t.Fatal("uncommitted responsibility caused deletion")
	}
}

func TestRecreatedRemoteServiceRecordsNewLabelBaselineBeforeReadFailure(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = "remote-recreated"
	service.Labels = map[string]string{"env": "v1"}
	if err := p.ensureActiveSyncState(service); err != nil {
		t.Fatal(err)
	}
	if err := p.persistAppliedServiceLabels(service, service.Labels); err != nil {
		t.Fatal(err)
	}
	service.Labels = map[string]string{"env": "v2", "temporary": "yes"}
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	remote := map[string]string{}
	created, failedRead := false, false
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/services" {
			switch r.Method {
			case http.MethodPost:
				if created {
					return aliasJSONResponse(http.StatusBadRequest, `{"code":400201,"info":"exists"}`)
				}
				var request []polarisapi.CreateServiceRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				remote, created = request[0].Metadata, true
			case http.MethodGet:
				if !failedRead {
					failedRead = true
					return nil, errors.New("GET failed after remote recreation")
				}
				return serviceMetadataResponse(t, remote), nil
			case http.MethodPut:
				var request []polarisapi.Service
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				remote = request[0].Metadata
			}
		}
		return successResponse(), nil
	}
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	if err := p.syncService(task); err == nil || !created {
		t.Fatal("fixture did not recreate remote Service before GET failure")
	}
	changed := service.DeepCopy()
	changed.Labels = map[string]string{"env": "v3"}
	storeObject(t, stores.services, changed)
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	if remote["env"] != "v3" || remote["temporary"] != "" {
		t.Fatalf("recreated Service lost its new label baseline: %v", remote)
	}
}

func TestSyncLogsKeepFullInfoSnapshotsAndLimitRepeatedFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.jsonl")
	options := *log.DefaultOptions()[log.SyncNamingLoggerName]
	options.OutputPaths, options.JSONEncoding = []string{path}, true
	if err := log.Configure(map[string]*log.Options{log.SyncNamingLoggerName: &options}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := log.Configure(map[string]*log.Options{
			log.SyncNamingLoggerName: log.DefaultOptions()[log.SyncNamingLoggerName],
		}); err != nil {
			t.Error(err)
		}
	})
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1", "10.0.0.2")
	changed := ownedInstance("10.0.0.2")
	changed.weight = 90
	p.consumer.(*testConsumer).instances = []model.Instance{changed, ownedInstance("10.0.0.3")}
	body := `{"code":200000}`
	tr.respond = func(*http.Request) (*http.Response, error) {
		return aliasJSONResponse(http.StatusOK, body)
	}
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	log.SyncNamingScope().ClearFailure(task.Key())
	t.Cleanup(func() { log.SyncNamingScope().ClearFailure(task.Key()) })
	run := func() {
		t.Helper()
		p.insertTask(task)
		if !p.handleTask(func(*Task) error { return p.processSyncInstance(service) }) {
			t.Fatal("worker stopped unexpectedly")
		}
	}
	run()
	readLogs := func() []map[string]interface{} {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		var entries []map[string]interface{}
		decoder := json.NewDecoder(file)
		for {
			var entry map[string]interface{}
			if err := decoder.Decode(&entry); err == io.EOF {
				return entries
			} else if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, entry)
		}
	}
	endpointInfo, sdkInfo, summaryInfo := false, false, false
	for _, entry := range readLogs() {
		message, _ := entry["msg"].(string)
		if entry["level"] == "warn" || entry["level"] == "error" {
			t.Fatalf("normal instance differences caused an alert: %v", entry)
		}
		if entry["level"] == "info" {
			endpoint, _ := entry["endpoint"].(string)
			endpointInfo = endpointInfo || strings.Contains(message, "get addresses map from endpoints") &&
				strings.Contains(endpoint, "10.0.0.1") && strings.Contains(endpoint, "10.0.0.2")
			sdkInfo = sdkInfo || strings.Contains(message, "Current polaris instance from sdk") &&
				strings.Contains(message, "10.0.0.2") && strings.Contains(message, "10.0.0.3")
			summaryInfo = summaryInfo || strings.Contains(message, "add=1 update=1 delete=1")
		}
	}
	if !endpointInfo || !sdkInfo || !summaryInfo {
		t.Fatalf("INFO snapshots lost detail: endpoint=%v sdk=%v summary=%v", endpointInfo, sdkInfo, summaryInfo)
	}
	body = `{"code":401000,"info":"injected unauthorized"}`
	for i := 0; i < 4; i++ {
		run()
	}
	body = `{"code":500123,"info":"injected storage failure"}`
	for i := 0; i < 2; i++ {
		run()
	}
	for p.queue.NumRequeues(task.QueueKey()) <= maxRetries {
		if p.queue.NumRequeues(task.QueueKey()) == maxRetries {
			run()
			break
		}
		run()
	}
	body = `{"code":200000}`
	run()
	errorsLogged, exhausted, recovered := 0, false, false
	for _, entry := range readLogs() {
		message, _ := entry["msg"].(string)
		if entry["level"] == "error" {
			errorsLogged++
			exhausted = exhausted || strings.Contains(message, "exhausted=true")
		}
		recovered = recovered || entry["level"] == "info" && strings.Contains(message, "recovered:")
	}
	if errorsLogged != 3 || !exhausted || !recovered {
		t.Fatalf("failure visibility wrong: errors=%d exhausted=%v recovered=%v", errorsLogged, exhausted, recovered)
	}
}
