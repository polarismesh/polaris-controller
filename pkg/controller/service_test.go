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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"k8s.io/client-go/util/workqueue"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestServiceDeleteRetriesReadFailureAndRetainsCache(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	p.queue.ShutDown()
	p.queue = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(time.Hour, time.Hour))
	defer p.queue.ShutDown()
	consumer := p.consumer.(*testConsumer)
	consumer.err = errors.New("discovery unavailable")
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationDelete}
	key := task.QueueKey()
	p.appliedServiceCache.Store(task.Key(), testService())
	p.insertTask(task)
	p.handleTask(p.process)
	if p.queue.NumRequeues(key) != 1 {
		t.Fatal("delete failure was not retried")
	}
	if p.queue.Len() != 0 {
		t.Fatal("delete failure bypassed rate limit")
	}
	if _, ok := p.appliedServiceCache.Load(task.Key()); !ok {
		t.Fatal("lost applied Service on discovery failure")
	}
	if len(tr.requests) != 0 {
		t.Fatal("wrote instances after failed discovery")
	}
	consumer.err = nil
	consumer.instances = []model.Instance{ownedInstance("10.0.0.1")}
	p.insertTask(task)
	p.handleTask(p.process)
	if p.queue.NumRequeues(key) != 0 {
		t.Fatal("success did not clear retries")
	}
	if _, ok := p.appliedServiceCache.Load(task.Key()); ok {
		t.Fatal("successful deletion did not clear applied cache")
	}
	if len(tr.requests) != 1 || tr.requests[0].path != "/naming/v1/instances/delete" {
		t.Fatalf("unexpected requests: %v", tr.requests)
	}
}

func TestFirstRegistrationFailureKeepsOnlyPersistedDeletionState(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/instances" {
			return nil, errors.New("partial registration failure")
		}
		return successResponse(), nil
	}
	svc := testService()
	storeObject(t, stores.services, svc)
	addTestEndpoint(t, stores, "10.0.0.1")
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationAdd}
	if err := p.syncService(task); err == nil {
		t.Fatal("registration error not returned")
	}
	if _, ok := p.appliedServiceCache.Load(task.Key()); ok {
		t.Fatal("failed registration advanced applied cache")
	}
	if _, found, err := p.syncStateStore.Get(context.Background(), "test|ns/svc"); err != nil || !found {
		t.Fatalf("partial registration lost persisted state: found=%v error=%v", found, err)
	}
	if err := stores.services.Delete(svc); err != nil {
		t.Fatal(err)
	}
	p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	cleanupTask, _ := takeQueueTask(t, p)
	if cleanupTask.ObjectType != PolarisSyncState {
		t.Fatalf("unexpected cleanup task: %+v", cleanupTask)
	}
	if err := p.process(cleanupTask); err != nil {
		t.Fatal(err)
	}
	last := tr.requests[len(tr.requests)-1]
	if last.path != "/naming/v1/instances/delete" {
		t.Fatalf("partial registration not cleaned up: %+v", last)
	}
}

func TestOnServiceUpdatePreparesSchedulingBeforeEnqueue(t *testing.T) {
	for _, tc := range []struct {
		name        string
		disableSync bool
		wantCached  bool
	}{
		{name: "sync to sync", wantCached: true},
		{name: "sync to disabled", disableSync: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestController(t)
			old := testService()
			cur := old.DeepCopy()
			if tc.disableSync {
				cur.Annotations[util.PolarisSync] = "false"
			} else {
				cur.Annotations[util.PolarisMetadata] = `{"version":"v2"}`
			}
			key := (&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}).Key()
			applied := old.DeepCopy()
			p.appliedServiceCache.Store(key, applied)

			observed := false
			baseQueue := p.queue
			p.queue = &observingQueue{
				RateLimitingInterface: baseQueue,
				onAdd: func() {
					observed = true
					_, cached := p.resyncServiceCache.Load(key)
					if cached != tc.wantCached {
						t.Fatalf("resync cache at enqueue=%v, want %v", cached, tc.wantCached)
					}
				},
			}
			p.onServiceUpdate(old, cur)
			if !observed {
				t.Fatal("service event was not enqueued")
			}
			current, found := p.appliedServiceCache.Load(key)
			if !found || current.Annotations[util.PolarisMetadata] != applied.Annotations[util.PolarisMetadata] {
				t.Fatal("event handler advanced applied cache")
			}
		})
	}
}

func TestServiceUpdateFailureKeepsAppliedCache(t *testing.T) {
	p, stores := newTestController(t)
	mockPolarisHTTP(t)
	old := testService()
	old.Annotations[util.PolarisMetadata] = `{"version":"old"}`
	cur := old.DeepCopy()
	cur.Annotations[util.PolarisMetadata] = `{"version":"new"}`
	storeObject(t, stores.services, cur)
	addTestEndpoint(t, stores, "10.0.0.1")
	p.consumer.(*testConsumer).err = errors.New("discovery unavailable")

	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	p.appliedServiceCache.Store(task.Key(), old)
	if err := p.syncService(task); err == nil {
		t.Fatal("update failure returned nil")
	}
	applied, found := p.appliedServiceCache.Load(task.Key())
	if !found || applied.Annotations[util.PolarisMetadata] != old.Annotations[util.PolarisMetadata] {
		t.Fatal("failed update advanced applied cache")
	}
}

func TestSuccessfulServiceSyncStoresAppliedDeepCopy(t *testing.T) {
	p, stores := newTestController(t)
	mockPolarisHTTP(t)
	service := testService()
	service.Annotations[util.PolarisMetadata] = `{"version":"applied"}`
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")

	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	service.Annotations[util.PolarisMetadata] = `{"version":"mutated"}`

	applied, found := p.appliedServiceCache.Load(task.Key())
	if !found || applied.Annotations[util.PolarisMetadata] != `{"version":"applied"}` {
		t.Fatal("applied cache shares the informer object")
	}
}

type observingQueue struct {
	workqueue.RateLimitingInterface
	onAdd func()
}

func (q *observingQueue) Add(item interface{}) {
	if q.onAdd != nil {
		q.onAdd()
	}
	q.RateLimitingInterface.Add(item)
}
