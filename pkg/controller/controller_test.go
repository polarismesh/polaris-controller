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
	"testing"
	"time"

	"k8s.io/client-go/util/workqueue"
)

func TestWorkerStopsOnQueueShutdown(t *testing.T) {
	p, _ := newTestController(t)
	done := make(chan struct{})
	go func() { p.worker(); close(done) }()
	p.queue.ShutDown()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after queue shutdown")
	}
	if p.handleTask(func(*Task) error { t.Error("processed a shut-down queue"); return nil }) {
		t.Fatal("shutdown must stop the worker loop")
	}
}

func TestMetricTrackerStops(t *testing.T) {
	p, _ := newTestController(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { p.MetricTracker(stop); close(done) }()
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("metric tracker did not stop before the next tick")
	}
}

func TestHandleTaskOwnsRateLimitedRetry(t *testing.T) {
	p, _ := newTestController(t)
	p.queue.ShutDown()
	p.queue = workqueue.NewRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(time.Hour, time.Hour))
	defer p.queue.ShutDown()
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	key := task.QueueKey()
	p.insertTask(task)
	if !p.handleTask(func(*Task) error { return errors.New("retry me") }) {
		t.Fatal("worker stopped")
	}
	if got := p.queue.NumRequeues(key); got != 1 {
		t.Fatalf("requeues=%d", got)
	}
	if p.queue.Len() != 0 {
		t.Fatal("failed task immediately requeued without backoff")
	}
	p.insertTask(task)
	p.handleTask(func(*Task) error { return nil })
	if p.queue.NumRequeues(key) != 0 {
		t.Fatal("successful task did not reset retry count")
	}
}

func TestWorkQueueDeduplicatesSameTask(t *testing.T) {
	p, _ := newTestController(t)
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationAdd}
	p.insertTask(task)
	task.Operation = OperationUpdate
	p.insertTask(task)
	if got := p.queue.Len(); got != 1 {
		t.Fatalf("queue length=%d want=1", got)
	}
}

func TestWorkQueueSeparatesObjectTypes(t *testing.T) {
	p, _ := newTestController(t)
	p.insertTask(&Task{Namespace: "ns", Name: "shared", ObjectType: KubernetesService})
	p.insertTask(&Task{Namespace: "ns", Name: "shared", ObjectType: KubernetesConfigMap})
	if got := p.queue.Len(); got != 2 {
		t.Fatalf("queue length=%d want=2", got)
	}
}

func TestEndpointAndServiceTasksShareQueueIdentity(t *testing.T) {
	p, _ := newTestController(t)
	p.insertTask(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService})
	p.insertTask(&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesEndpoints})
	if got := p.queue.Len(); got != 1 {
		t.Fatalf("queue length=%d want=1 after endpoint normalization", got)
	}
	if !p.handleTask(func(task *Task) error {
		if task.ObjectType != KubernetesService {
			t.Fatalf("restored object type=%v want %v", task.ObjectType, KubernetesService)
		}
		return nil
	}) {
		t.Fatal("worker stopped while processing endpoint task")
	}
}

func TestWorkQueueSerializesSameTaskWhileProcessing(t *testing.T) {
	p, _ := newTestController(t)
	key := (&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}).QueueKey()
	p.queue.Add(key)
	first, _ := p.queue.Get()
	p.queue.Add(key)
	if got := p.queue.Len(); got != 0 {
		t.Fatalf("processing key became concurrently available: len=%d", got)
	}
	p.queue.Done(first)
	if got := p.queue.Len(); got != 1 {
		t.Fatalf("dirty key was not requeued after Done: len=%d", got)
	}
	second, _ := p.queue.Get()
	p.queue.Done(second)
}
