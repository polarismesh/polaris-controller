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
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func waitForQueueLength(t *testing.T, p *PolarisController, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p.queue.Len() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("queue length=%d, want %d", p.queue.Len(), want)
}

func TestServiceAddWaitsForNamespaceCache(t *testing.T) {
	p, stores := newTestController(t)
	service := testService()
	delete(service.Annotations, util.PolarisSync)
	storeObject(t, stores.services, service)

	namespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: service.Namespace,
		Annotations: map[string]string{util.PolarisSync: util.IsEnableSync}}}
	storeObject(t, stores.namespaces, namespace)

	namespaceSynced := false
	p.namespaceSynced = func() bool { return namespaceSynced }
	p.onServiceAdd(service)
	if p.queue.Len() != 0 {
		t.Fatalf("service was queued before the Namespace cache synced: len=%d", p.queue.Len())
	}
	if _, ok := p.resyncServiceCache.Load((&Task{
		Namespace: service.Namespace, Name: service.Name, ObjectType: KubernetesService,
	}).Key()); ok {
		t.Fatal("service entered resync cache before eligibility was known")
	}

	namespaceSynced = true
	waitForQueueLength(t, p, 1)
	task, _ := takeQueueTask(t, p)
	if task.ObjectType != KubernetesService ||
		task.Namespace != service.Namespace || task.Name != service.Name {
		t.Fatalf("unexpected delayed task: %+v", task)
	}
	if !p.IsPolarisService(service) {
		t.Fatal("service was not eligible after the Namespace cache synced")
	}
}

func TestServiceAddWithExplicitSyncDoesNotWaitForNamespaceCache(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()

	p.namespaceSynced = func() bool { return false }
	p.onServiceAdd(service)
	if p.queue.Len() != 1 {
		t.Fatalf("explicit service sync was delayed: len=%d", p.queue.Len())
	}
}

func TestServiceAddWithMissingNamespaceAfterCacheSyncDoesNotQueue(t *testing.T) {
	p, _ := newTestController(t)
	service := testService()
	delete(service.Annotations, util.PolarisSync)

	p.namespaceSynced = func() bool { return true }
	p.onServiceAdd(service)
	if p.queue.Len() != 0 {
		t.Fatalf("missing Namespace was queued as a retry: len=%d", p.queue.Len())
	}
	if p.IsPolarisService(service) {
		t.Fatal("service without a Namespace became eligible for synchronization")
	}
}

func TestEndpointEventWaitsForNamespaceCache(t *testing.T) {
	p, _ := newTestController(t)
	endpoints := &v1.Endpoints{ObjectMeta: metav1.ObjectMeta{
		Namespace: "ns", Name: "svc"}}

	p.namespaceSynced = func() bool { return false }
	p.onEndpointAdd(endpoints)
	if p.queue.Len() != 0 {
		t.Fatalf("endpoint was queued before the Namespace cache synced: len=%d", p.queue.Len())
	}

	waitForQueueLength(t, p, 1)
	task, _ := takeQueueTask(t, p)
	if task.ObjectType != KubernetesService ||
		task.Namespace != endpoints.Namespace || task.Name != endpoints.Name {
		t.Fatalf("unexpected delayed endpoint task: %+v", task)
	}
}
