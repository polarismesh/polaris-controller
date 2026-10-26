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
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"k8s.io/client-go/util/workqueue"
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
	p.serviceCache.Store(task.Key(), testService())
	p.queue.Add(task)
	p.handleTask(p.process)
	if p.queue.NumRequeues(task) != 1 {
		t.Fatal("delete failure was not retried")
	}
	if p.queue.Len() != 0 {
		t.Fatal("delete failure bypassed rate limit")
	}
	if _, ok := p.serviceCache.Load(task.Key()); !ok {
		t.Fatal("lost cached Service on discovery failure")
	}
	if len(tr.requests) != 0 {
		t.Fatal("wrote instances after failed discovery")
	}
	consumer.err = nil
	consumer.instances = []model.Instance{ownedInstance("10.0.0.1")}
	p.queue.Add(task)
	p.handleTask(p.process)
	if p.queue.NumRequeues(task) != 0 {
		t.Fatal("success did not clear retries")
	}
	if _, ok := p.serviceCache.Load(task.Key()); ok {
		t.Fatal("successful deletion did not clear cache")
	}
	if len(tr.requests) != 1 || tr.requests[0].path != "/naming/v1/instances/delete" {
		t.Fatalf("unexpected requests: %v", tr.requests)
	}
}

func TestFirstRegistrationFailureRetainsDeletionState(t *testing.T) {
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
	if _, ok := p.serviceCache.Load(task.Key()); !ok {
		t.Fatal("partial registration lost deletion state")
	}
	if err := stores.services.Delete(svc); err != nil {
		t.Fatal(err)
	}
	p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}
	task.Operation = OperationDelete
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	last := tr.requests[len(tr.requests)-1]
	if last.path != "/naming/v1/instances/delete" {
		t.Fatalf("partial registration not cleaned up: %+v", last)
	}
}
