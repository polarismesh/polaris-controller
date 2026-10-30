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
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestSyncStateStorePersistsAcrossStoreInstances(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-state")

	if err := p.ensureActiveSyncState(service); err != nil {
		t.Fatal(err)
	}
	reloaded := newSyncStateStore(p.client)
	records, err := reloaded.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Phase != syncStateActive || records[0].SourceUID != "uid-state" {
		t.Fatalf("unexpected active records: %+v", records)
	}

	if err = p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}
	reloaded = newSyncStateStore(p.client)
	records, err = reloaded.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Phase != syncStatePendingDelete {
		t.Fatalf("unexpected pending records: %+v", records)
	}

	if err = p.syncStateStore.Remove(context.Background(), records[0]); err != nil {
		t.Fatal(err)
	}
	reloaded = newSyncStateStore(p.client)
	records, err = reloaded.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records not removed: %+v", records)
	}
}

func TestRecoverSyncStatesMarksMissingActiveRecordPending(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-missing")
	if err := p.ensureActiveSyncState(service); err != nil {
		t.Fatal(err)
	}

	if err := p.recoverSyncStates(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-missing")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStatePendingDelete {
		t.Fatalf("record=%+v found=%v", record, found)
	}
	task, _ := takeQueueTask(t, p)
	if task.ObjectType != PolarisSyncState || task.Name != "test|uid-missing" {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestRecoverSyncStatesMarksOldUIDPendingWhenServiceWasRecreated(t *testing.T) {
	p, stores := newTestController(t)
	oldService := testService()
	oldService.UID = types.UID("uid-old")
	if err := p.ensureActiveSyncState(oldService); err != nil {
		t.Fatal(err)
	}

	newService := testService()
	newService.UID = types.UID("uid-new")
	storeObject(t, stores.services, newService)
	if err := p.recoverSyncStates(context.Background()); err != nil {
		t.Fatal(err)
	}

	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-old")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStatePendingDelete {
		t.Fatalf("old record=%+v found=%v", record, found)
	}
	task, _ := takeQueueTask(t, p)
	if task.ObjectType != PolarisSyncState || task.Name != "test|uid-old" {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestRecoverSyncStatesReactivatesPendingRecord(t *testing.T) {
	p, stores := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-active")
	storeObject(t, stores.services, service)
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}

	if err := p.recoverSyncStates(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-active")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStateActive {
		t.Fatalf("record=%+v found=%v", record, found)
	}
	if p.queue.Len() != 0 {
		t.Fatal("active service was queued for deletion")
	}
}

func TestProcessPendingSyncStateDeletesAndRemovesRecord(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = types.UID("uid-pending")
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}
	instance := ownedInstance("10.0.0.1")
	instance.metadata[util.K8sServiceUID] = "uid-pending"
	newGeneration := ownedInstance("10.0.0.2")
	newGeneration.metadata[util.K8sServiceUID] = "uid-new"
	p.consumer.(*testConsumer).instances = []model.Instance{instance, newGeneration}
	mockPolarisInstances(tr, p.consumer.(*testConsumer))

	task := newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()
	if err := p.processPendingSyncState(task); err != nil {
		t.Fatal(err)
	}
	if _, found, err := p.syncStateStore.Get(context.Background(), "test|uid-pending"); err != nil || found {
		t.Fatalf("record found=%v error=%v", found, err)
	}
	writes := tr.writes()
	if len(writes) != 1 || writes[0].path != "/naming/v1/instances/delete" {
		t.Fatalf("unexpected delete requests: %+v", tr.requests)
	}
	var payload []polarisapi.Instance
	if err := json.Unmarshal(writes[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Host != "10.0.0.1" {
		t.Fatalf("pending cleanup removed another Service generation: %+v", payload)
	}
}

func TestProcessPendingSyncStateSkipsEndpointOwnedByRecreatedService(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	oldService := testService()
	oldService.UID = types.UID("uid-old")
	if err := p.markPendingSyncState(oldService); err != nil {
		t.Fatal(err)
	}

	currentService := oldService.DeepCopy()
	currentService.UID = types.UID("uid-new")
	storeObject(t, stores.services, currentService)
	addTestEndpoint(t, stores, "10.0.0.1")

	oldInstance := ownedInstance("10.0.0.1")
	oldInstance.metadata[util.K8sServiceUID] = "uid-old"
	consumer := p.consumer.(*testConsumer)
	consumer.instances = []model.Instance{oldInstance}
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/naming/v1/instances/delete" {
			return nil, errors.New("pending cleanup deleted an endpoint owned by the new Service UID")
		}
		if request.Method == http.MethodPut && request.URL.Path == "/naming/v1/instances" {
			var instances []polarisapi.Instance
			if err := json.NewDecoder(request.Body).Decode(&instances); err != nil {
				return nil, err
			}
			if len(instances) == 1 {
				updated := ownedInstance(instances[0].Host)
				updated.metadata = instances[0].Metadata
				consumer.instances = []model.Instance{updated}
			}
		}
		return successResponse(), nil
	}
	mockPolarisInstances(tr, consumer)

	task := newServiceSyncState(oldService, "test", syncStatePendingDelete).sourceTask()
	if err := p.processPendingSyncState(task); err != nil {
		t.Fatal(err)
	}
	if len(tr.writes()) != 0 {
		t.Fatalf("pending cleanup deleted an endpoint owned by the new Service UID: %+v", tr.requests)
	}
	if record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-old"); err != nil || !found || record.Phase != syncStatePendingDelete {
		t.Fatalf("old cleanup responsibility lost: state=%+v found=%v error=%v", record, found, err)
	}
	newRecord, found, err := p.syncStateStore.Get(context.Background(), "test|uid-new")
	if err != nil || !found || newRecord.Phase != syncStateActive {
		t.Fatalf("new Service state=%+v found=%v error=%v", newRecord, found, err)
	}
	if err := p.syncService(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}); err != nil {
		t.Fatal(err)
	}
	if len(consumer.instances) != 1 ||
		consumer.instances[0].GetMetadata()[util.K8sServiceUID] != "uid-new" {
		t.Fatalf("new Service did not take over the endpoint: %+v", consumer.instances)
	}
	if err := p.processPendingSyncState(task); err != nil {
		t.Fatal(err)
	}
	if _, found, err := p.syncStateStore.Get(context.Background(), "test|uid-old"); err != nil || found {
		t.Fatalf("confirmed takeover did not release old state: found=%v error=%v", found, err)
	}
}

func TestProcessPendingSyncStateReactivatesCurrentService(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = types.UID("uid-reenabled")
	storeObject(t, stores.services, service)
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}

	instance := ownedInstance("10.0.0.1")
	instance.metadata[util.K8sServiceUID] = string(service.UID)
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	task := newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()
	if err := p.processPendingSyncState(task); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("reactivated Service was deleted: %+v", tr.requests)
	}
	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-reenabled")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStateActive {
		t.Fatalf("record=%+v found=%v", record, found)
	}
}

func TestRemoveSyncStateIfMatchKeepsReactivatedRecord(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-cas")
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}
	pending, found, err := p.syncStateStore.Get(context.Background(), "test|uid-cas")
	if err != nil || !found {
		t.Fatalf("pending record found=%v error=%v", found, err)
	}
	if err = p.ensureActiveSyncState(service); err != nil {
		t.Fatal(err)
	}
	removed, err := p.syncStateStore.RemoveIfMatch(context.Background(), pending)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("stale pending record removed the reactivated state")
	}
	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-cas")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStateActive {
		t.Fatalf("record=%+v found=%v", record, found)
	}
}

func TestProcessPendingSyncStateRetainsRecordOnFailure(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = types.UID("uid-retry")
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}
	p.consumer.(*testConsumer).err = errors.New("discovery unavailable")
	mockPolarisInstances(tr, p.consumer.(*testConsumer))

	task := newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()
	if err := p.processPendingSyncState(task); err == nil {
		t.Fatal("pending deletion failure returned nil")
	}
	record, found, err := p.syncStateStore.Get(context.Background(), "test|uid-retry")
	if err != nil {
		t.Fatal(err)
	}
	if !found || record.Phase != syncStatePendingDelete || record.Attempts != 1 {
		t.Fatalf("pending record was not retained: %+v, found=%v", record, found)
	}
}

type blockingStateTransport struct {
	started chan bool
	release chan struct{}
	once    sync.Once
}

func (tr *blockingStateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	tr.once.Do(func() {
		_, hasDeadline := request.Context().Deadline()
		tr.started <- hasDeadline
		<-tr.release
	})
	body := `{"apiVersion":"v1","kind":"ConfigMap",` +
		`"metadata":{"name":"polaris-controller-sync-state-00","namespace":"polaris-system","resourceVersion":"1"},` +
		`"data":{"records":"{}"}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}, nil
}

func TestSyncStateNetworkRequestDoesNotBlockUnrelatedService(t *testing.T) {
	p, _ := newTestController(t)
	transport := &blockingStateTransport{
		started: make(chan bool, 1),
		release: make(chan struct{}),
	}
	var release sync.Once
	unblock := func() { release.Do(func() { close(transport.release) }) }
	t.Cleanup(unblock)
	client, err := kubernetes.NewForConfigAndClient(
		&rest.Config{Host: "http://sync-state.invalid"}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	p.syncStateStore.client = client
	p.syncStateStore.initialized = true
	p.syncStateStore.records = make(map[string]serviceSyncState)

	service := testService()
	service.UID = types.UID("uid-network")
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- p.ensureActiveSyncState(service)
	}()

	var hasDeadline bool
	select {
	case hasDeadline = <-transport.started:
	case <-time.After(time.Second):
		unblock()
		<-writeDone
		t.Fatal("state write did not reach the Kubernetes request")
	}

	readDone := make(chan error, 1)
	go func() {
		_, _, readErr := p.syncStateStore.Get(context.Background(), "test|uid-unrelated")
		readDone <- readErr
	}()
	select {
	case err = <-readDone:
		if err != nil {
			unblock()
			<-writeDone
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		unblock()
		<-writeDone
		t.Fatal("unrelated Service state lookup blocked on another record's network request")
	}

	unblock()
	if err = <-writeDone; err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("sync state request did not carry a deadline")
	}
}

func TestSyncStateGetDoesNotBlockOnRefresh(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-refresh")
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}

	transport := &blockingStateTransport{
		started: make(chan bool, 1),
		release: make(chan struct{}),
	}
	var release sync.Once
	unblock := func() { release.Do(func() { close(transport.release) }) }
	t.Cleanup(unblock)
	client, err := kubernetes.NewForConfigAndClient(
		&rest.Config{Host: "http://sync-state-refresh.invalid"}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	p.syncStateStore.client = client

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- p.syncStateStore.Refresh(context.Background())
	}()
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		unblock()
		<-refreshDone
		t.Fatal("refresh did not reach the Kubernetes request")
	}

	getDone := make(chan error, 1)
	go func() {
		_, _, getErr := p.syncStateStore.Get(context.Background(), "test|uid-refresh")
		getDone <- getErr
	}()
	select {
	case err = <-getDone:
		if err != nil {
			unblock()
			<-refreshDone
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		unblock()
		<-refreshDone
		t.Fatal("state lookup blocked behind a network-bound refresh")
	}

	unblock()
	if err = <-refreshDone; err != nil {
		t.Fatal(err)
	}
}

func TestEnsureActiveSyncStateFailureBlocksPolarisWrites(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	client := p.client.(*fake.Clientset)
	client.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("configmap unavailable")
	})

	service := testService()
	service.UID = types.UID("uid-failure")
	if err := p.processSyncNamespaceAndService(service); err == nil {
		t.Fatal("sync state persistence failure did not block Polaris writes")
	}
	if len(tr.requests) != 0 {
		t.Fatalf("Polaris was written before sync state persisted: %+v", tr.requests)
	}
}

func TestOnServiceDeleteHandlesTombstone(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-tombstone")
	p.onServiceDelete(cache.DeletedFinalStateUnknown{Obj: service})

	task, _ := takeQueueTask(t, p)
	if task.Namespace != service.Namespace || task.Name != service.Name ||
		task.ObjectType != KubernetesService {
		t.Fatalf("unexpected tombstone task: %+v", task)
	}
}

func TestStateConfigMapIsNotSyncedToPolaris(t *testing.T) {
	p, _ := newTestController(t)
	configMap := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default",
		Name:      "polaris-controller-sync-state-00",
		Labels:    map[string]string{syncStateLabelKey: syncStateLabelValue},
	}}
	if p.IsPolarisConfigMap(configMap) {
		t.Fatal("sync state ConfigMap was treated as user config")
	}
}
