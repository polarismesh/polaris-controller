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
	"errors"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

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
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	task := newServiceSyncState(service, "test", syncStatePendingDelete).sourceTask()
	if err := p.processPendingSyncState(task); err != nil {
		t.Fatal(err)
	}
	if _, found, err := p.syncStateStore.Get(context.Background(), "test|uid-pending"); err != nil || found {
		t.Fatalf("record found=%v error=%v", found, err)
	}
	if len(tr.requests) != 1 || tr.requests[0].path != "/naming/v1/instances/delete" {
		t.Fatalf("unexpected delete requests: %+v", tr.requests)
	}
}

func TestProcessPendingSyncStateRetainsRecordOnFailure(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	service.UID = types.UID("uid-retry")
	if err := p.markPendingSyncState(service); err != nil {
		t.Fatal(err)
	}
	p.consumer.(*testConsumer).err = errors.New("discovery unavailable")

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
