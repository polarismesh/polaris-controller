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
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestResyncUsesFullCacheKeysAndObjectTypes(t *testing.T) {
	for _, kind := range []ObjectType{KubernetesService, KubernetesConfigMap} {
		for _, name := range []string{"b", "long-service-name"} {
			for _, cached := range []bool{false, true} {
				p, _ := newTestController(t)
				task := &Task{Namespace: "a", Name: name, ObjectType: kind}
				meta := metav1.ObjectMeta{Namespace: "a", Name: name, Annotations: map[string]string{util.PolarisSync: "true"}}
				if kind == KubernetesService {
					svc := testService()
					svc.ObjectMeta = meta
					p.resyncServiceCache.Store(task.Key(), svc)
					if cached {
						p.appliedServiceCache.Store(task.Key(), svc)
					}
				} else {
					cm := &v1.ConfigMap{ObjectMeta: meta}
					p.resyncConfigFileCache.Store(task.Key(), cm)
					if cached {
						p.configFileCache.Store(task.Key(), cm)
					}
				}
				p.resyncWorker()
				if p.queue.Len() != 1 {
					t.Fatalf("kind=%v name=%s cached=%v: tasks=%d", kind, name, cached, p.queue.Len())
				}
				got, _ := takeQueueTask(t, p)
				if got.ObjectType != kind || got.Key() != task.Key() {
					t.Fatalf("wrong resync task: %+v", got)
				}
			}
		}
	}
}

func TestResyncSkipsUnhealthyServerAndDisabledConfigSync(t *testing.T) {
	p, _ := newTestController(t)
	svc := testService()
	task := &Task{Namespace: svc.Namespace, Name: svc.Name, ObjectType: KubernetesService}
	p.resyncServiceCache.Store(task.Key(), svc)
	p.isPolarisServerHealthy.Store(false)
	p.resyncWorker()
	if p.queue.Len() != 0 {
		t.Fatal("resync ran against an unhealthy server")
	}
	p.resyncServiceCache.Clear()
	cm := &v1.ConfigMap{ObjectMeta: svc.ObjectMeta}
	task.ObjectType = KubernetesConfigMap
	p.resyncConfigFileCache.Store(task.Key(), cm)
	p.config.PolarisController.ConfigSync.Enable = false
	p.isPolarisServerHealthy.Store(true)
	p.resyncWorker()
	if p.queue.Len() != 0 {
		t.Fatal("configuration resync ran while disabled")
	}
}

func TestHealthRecoveryKeepsPendingServiceLabelChanges(t *testing.T) {
	for _, operation := range []string{"changed", "removed", "unchanged external drift"} {
		t.Run(operation, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			old := testService()
			old.UID = "uid-label"
			old.Labels = map[string]string{"env": "v1", "stable": "old"}
			current := old.DeepCopy()
			if operation == "changed" {
				current.Labels["env"] = "v2"
			} else if operation == "removed" {
				delete(current.Labels, "env")
			}
			storeObject(t, stores.services, current)
			addTestEndpoint(t, stores, "10.0.0.1")
			task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
			p.appliedServiceCache.Store(task.Key(), old.DeepCopy())
			p.resyncServiceCache.Store(task.Key(), current.DeepCopy())
			instance := ownedInstance("10.0.0.1")
			instance.metadata[util.K8sServiceUID] = "uid-label"
			p.consumer.(*testConsumer).instances = []model.Instance{instance}
			remote := map[string]string{"env": "v1", "stable": "manual-drift", "manual": "keep"}
			tr.respond = func(request *http.Request) (*http.Response, error) {
				switch {
				case request.URL.Path == "/naming/v1/services" && request.Method == http.MethodPost:
					return &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{"code":400201,"info":"already exists"}`))}, nil
				case request.URL.Path == "/naming/v1/services" && request.Method == http.MethodGet:
					return serviceMetadataResponse(t, remote), nil
				case request.URL.Path == "/naming/v1/services" && request.Method == http.MethodPut:
					var payload []polarisapi.Service
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						return nil, err
					}
					remote = payload[0].Metadata
				}
				return successResponse(), nil
			}
			p.isPolarisServerHealthy.Store(false)
			p.checkHealth()
			applied, found := p.appliedServiceCache.Load(task.Key())
			if !found || !maps.Equal(applied.Labels, old.Labels) {
				t.Fatal("health recovery discarded the last-applied Label baseline")
			}
			p.resyncWorker()
			queued, _ := takeQueueTask(t, p)
			if err := p.process(queued); err != nil {
				t.Fatal(err)
			}
			if err := p.syncService(task); err != nil {
				t.Fatal(err)
			}
			value, exists := remote["env"]
			if (operation == "changed" && value != "v2") || (operation == "removed" && exists) ||
				(operation == "unchanged external drift" && value != "v1") {
				t.Fatalf("Labels did not converge after recovery: %+v", remote)
			}
			if remote["manual"] != "keep" || remote["stable"] != "manual-drift" {
				t.Fatalf("recovery overwrote external metadata: %+v", remote)
			}
			if got := countRequests(tr.requests, http.MethodPost, "/naming/v1/services"); got != 1 {
				t.Fatalf("Namespace/Service recovery checks=%d want=1", got)
			}
		})
	}
}

func TestHealthRecoveryRetriesServiceRecreation(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.Labels = map[string]string{"env": "v2"}
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores)
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	applied := service.DeepCopy()
	applied.Labels["env"] = "v1"
	p.appliedServiceCache.Store(task.Key(), applied)
	failCreate := true
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost && request.URL.Path == "/naming/v1/services" && failCreate {
			return nil, errors.New("Service recreation unavailable")
		}
		return successResponse(), nil
	}
	p.isPolarisServerHealthy.Store(false)
	p.checkHealth()
	if err := p.syncService(task); err == nil {
		t.Fatal("expected failed Service recreation")
	}
	if baseline, _ := p.appliedServiceCache.Load(task.Key()); baseline.Labels["env"] != "v1" {
		t.Fatal("failed recovery advanced Label baseline")
	}
	if _, found := p.serviceRecoveryApplied.Load(task.Key()); found {
		t.Fatal("failed Service recreation advanced recovery generation")
	}
	failCreate = false
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	if got := countRequests(tr.requests, http.MethodPost, "/naming/v1/services"); got != 2 {
		t.Fatalf("failed Service recreation did not retry: attempts=%d", got)
	}
}

func TestHealthRecoveryDuringServiceSyncIsNotLost(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores)
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}
	p.appliedServiceCache.Store(task.Key(), service.DeepCopy())
	p.isPolarisServerHealthy.Store(false)
	p.checkHealth()
	recoveredDuringWrite := false
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost && request.URL.Path == "/naming/v1/services" && !recoveredDuringWrite {
			recoveredDuringWrite = true
			p.isPolarisServerHealthy.Store(false)
			p.checkHealth()
		}
		return successResponse(), nil
	}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	if got := countRequests(tr.requests, http.MethodPost, "/naming/v1/services"); got != 2 {
		t.Fatalf("health recovery during writes was lost: checks=%d", got)
	}
}
