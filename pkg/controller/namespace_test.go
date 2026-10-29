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
	"fmt"
	"net/http"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-go/pkg/model"
)

func TestNamespaceSyncTransitions(t *testing.T) {
	for _, mode := range []string{util.SyncModeAll, util.SyncModeDemand} {
		for _, direction := range []string{util.SyncDirectionBoth, util.SyncDirectionKubernetesToPolaris, util.SyncDirectionPolarisToKubernetes, "disabled"} {
			for _, enabled := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/%v", mode, direction, enabled), func(t *testing.T) {
					p, stores := newTestController(t)
					p.config.PolarisController.SyncMode = mode
					p.config.PolarisController.ConfigSync.SyncDirection = direction
					p.config.PolarisController.ConfigSync.Enable = direction != "disabled"
					configEnabled := direction == util.SyncDirectionBoth || direction == util.SyncDirectionKubernetesToPolaris
					oldNs := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: fmt.Sprint(!enabled)}}}
					curNs := oldNs.DeepCopy()
					curNs.Annotations[util.PolarisSync] = fmt.Sprint(enabled)
					storeObject(t, stores.namespaces, curNs)
					for _, setting := range []string{"inherited", "true", "false", "invalid"} {
						svc := testService()
						svc.Name = setting
						svc.Annotations = map[string]string{}
						if setting != "inherited" {
							svc.Annotations[util.PolarisSync] = setting
						}
						cm := &v1.ConfigMap{ObjectMeta: *svc.ObjectMeta.DeepCopy()}
						storeObject(t, stores.services, svc)
						storeObject(t, stores.configs, cm)
						if !enabled && (setting == "inherited" || setting == "true") {
							key := (&Task{Namespace: "ns", Name: setting, ObjectType: KubernetesService}).Key()
							p.resyncServiceCache.Store(key, svc)
							key = (&Task{Namespace: "ns", Name: setting, ObjectType: KubernetesConfigMap}).Key()
							p.resyncConfigFileCache.Store(key, cm)
						}
					}
					invalid := testService()
					invalid.Name = "no-selector"
					invalid.Spec.Selector = nil
					storeObject(t, stores.services, invalid)
					foreign := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "from-polaris",
						Annotations: map[string]string{util.InternalConfigFileSyncSourceKey: util.SourceFromPolaris}}}
					storeObject(t, stores.configs, foreign)
					p.onNamespaceUpdate(oldNs, curNs)
					wantNames := map[string]bool{"inherited": true}
					if enabled {
						wantNames["true"] = true
					}
					found := map[string]bool{}
					for p.queue.Len() > 0 {
						task, _ := takeQueueTask(t, p)
						if task.ObjectType == KubernetesNamespace {
							continue
						}
						if !wantNames[task.Name] {
							t.Fatalf("unexpected task: %+v", task)
						}
						if task.ObjectType == KubernetesConfigMap && !configEnabled {
							t.Fatal("configuration task in disabled direction")
						}
						found[task.Key()] = true
					}
					for name := range wantNames {
						key := (&Task{Namespace: "ns", Name: name, ObjectType: KubernetesService}).Key()
						if !found[key] {
							t.Fatalf("missing Service task: %s", key)
						}
						if _, ok := p.resyncServiceCache.Load(key); ok != enabled {
							t.Fatalf("Service resync cache=%v want=%v", ok, enabled)
						}
						key = (&Task{Namespace: "ns", Name: name, ObjectType: KubernetesConfigMap}).Key()
						if configEnabled {
							if !found[key] {
								t.Fatalf("missing ConfigMap task: %s", key)
							}
							if _, ok := p.resyncConfigFileCache.Load(key); ok != enabled {
								t.Fatalf("ConfigMap resync cache=%v want=%v", ok, enabled)
							}
						}
					}
					if !enabled {
						key := (&Task{Namespace: "ns", Name: "true", ObjectType: KubernetesService}).Key()
						if _, ok := p.resyncServiceCache.Load(key); !ok {
							t.Fatal("explicit Service sync override removed")
						}
					}
				})
			}
		}
	}
}

func TestNamespaceAddTaskUsesLiveAnnotations(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			svc := testService()
			svc.Annotations = nil
			ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: fmt.Sprint(enabled)}}}
			storeObject(t, stores.namespaces, ns)
			storeObject(t, stores.services, svc)
			addTestEndpoint(t, stores, "10.0.0.1")
			task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationAdd}
			if err := p.syncService(task); err != nil {
				t.Fatal(err)
			}
			if enabled && len(tr.requests) != 3 {
				t.Fatalf("expected namespace, service, instance creation; got %d", len(tr.requests))
			}
			if !enabled && len(tr.requests) != 0 {
				t.Fatal("stale Add task forced sync after Namespace disabled")
			}
			if svc.Annotations != nil {
				t.Fatal("mutated informer Service")
			}
		})
	}
}

func TestNamespaceConfigMapTaskDoesNotMutateInformer(t *testing.T) {
	p, stores := newTestController(t)
	mockPolarisHTTP(t)
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: "true"}}}
	cm := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "config"}, Data: map[string]string{"key": "value"}}
	storeObject(t, stores.namespaces, ns)
	storeObject(t, stores.configs, cm)
	task := &Task{Namespace: "ns", Name: "config", ObjectType: KubernetesConfigMap, Operation: OperationAdd}
	if err := p.syncConfigMap(task); err != nil {
		t.Fatal(err)
	}
	task.Operation = OperationUpdate
	if err := p.syncConfigMap(task); err != nil {
		t.Fatal(err)
	}
	if cm.Annotations != nil || cm.Labels != nil {
		t.Fatal("mutated informer ConfigMap")
	}
}

func TestNamespaceDisableDeletesInheritedInstances(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: "true"}}}
	svc := testService()
	svc.Annotations = nil
	storeObject(t, stores.namespaces, ns)
	storeObject(t, stores.services, svc)
	addTestEndpoint(t, stores, "10.0.0.1")
	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationAdd}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}
	disabled := ns.DeepCopy()
	disabled.Annotations[util.PolarisSync] = "false"
	storeObject(t, stores.namespaces, disabled)
	p.onNamespaceUpdate(ns, disabled)
	if p.queue.Len() != 1 {
		t.Fatalf("expected inherited Service delete task, got %d", p.queue.Len())
	}
	p.handleTask(p.process)
	last := tr.requests[len(tr.requests)-1]
	if last.path != "/naming/v1/instances/delete" {
		t.Fatalf("Namespace disable did not delete instances: %+v", last)
	}
}

func TestNamespaceDisableKeepsEnableRegisterServiceInResync(t *testing.T) {
	p, stores := newTestController(t)
	ns := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: "true"}}}
	svc := testService()
	svc.Annotations = map[string]string{util.PolarisEnableRegister: util.IsEnableSync}
	storeObject(t, stores.namespaces, ns)
	storeObject(t, stores.services, svc)
	key := (&Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService}).Key()
	p.resyncServiceCache.Store(key, svc)

	disabled := ns.DeepCopy()
	disabled.Annotations[util.PolarisSync] = "false"
	storeObject(t, stores.namespaces, disabled)
	p.onNamespaceUpdate(ns, disabled)

	if p.queue.Len() != 0 {
		t.Fatalf("enableRegister Service should not receive Namespace delete task, got %d", p.queue.Len())
	}
	if _, ok := p.resyncServiceCache.Load(key); !ok {
		t.Fatal("Namespace disable removed enableRegister Service from resync cache")
	}
	if !p.IsPolarisService(svc) {
		t.Fatal("enableRegister Service lost sync eligibility")
	}
}

func TestConfigMapDeletionRetainsDefaultGroup(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	p.config.PolarisController.ConfigSync.DefaultGroup = "custom-group"
	cm := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "config",
		Annotations: map[string]string{util.PolarisSync: "true"}}}
	storeObject(t, stores.configs, cm)
	task := &Task{Namespace: "ns", Name: "config", ObjectType: KubernetesConfigMap, Operation: OperationAdd}
	if err := p.syncConfigMap(task); err != nil {
		t.Fatal(err)
	}
	changed := cm.DeepCopy()
	changed.Data = map[string]string{"key": "new"}
	storeObject(t, stores.configs, changed)
	p.onConfigMapUpdate(cm, changed)
	p.handleTask(p.process)
	if err := stores.configs.Delete(changed); err != nil {
		t.Fatal(err)
	}
	p.onConfigMapDelete(changed)
	p.handleTask(p.process)
	last := tr.requests[len(tr.requests)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("expected delete: %+v", last)
	}
	var payload polarisapi.ConfigFile
	if err := json.Unmarshal(last.body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Group != "custom-group" {
		t.Fatalf("delete group=%q want custom-group", payload.Group)
	}
	if cm.Annotations[util.PolarisConfigGroup] != "" {
		t.Fatal("modified informer group")
	}
}

func TestNamespaceAnnotationRemovalUsesSyncModeDefault(t *testing.T) {
	for _, tc := range []struct {
		mode, old  string
		wantCached bool
	}{
		{util.SyncModeAll, "false", true},
		{util.SyncModeDemand, "true", false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			p, stores := newTestController(t)
			p.config.PolarisController.SyncMode = tc.mode
			old := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns", Annotations: map[string]string{util.PolarisSync: tc.old}}}
			cur := old.DeepCopy()
			cur.Annotations = nil
			storeObject(t, stores.namespaces, cur)
			svc := testService()
			svc.Annotations = nil
			cm := &v1.ConfigMap{ObjectMeta: *svc.ObjectMeta.DeepCopy()}
			storeObject(t, stores.services, svc)
			storeObject(t, stores.configs, cm)
			p.onNamespaceUpdate(old, cur)
			objects := 0
			for p.queue.Len() > 0 {
				task, _ := takeQueueTask(t, p)
				if task.ObjectType == KubernetesNamespace {
					continue
				}
				objects++
			}
			if objects != 2 {
				t.Fatalf("Service/ConfigMap tasks=%d want=2", objects)
			}
			serviceKey := (&Task{Namespace: "ns", Name: svc.Name, ObjectType: KubernetesService}).Key()
			if _, ok := p.resyncServiceCache.Load(serviceKey); ok != tc.wantCached {
				t.Fatalf("Service cache=%v want=%v", ok, tc.wantCached)
			}
			configKey := (&Task{Namespace: "ns", Name: cm.Name, ObjectType: KubernetesConfigMap}).Key()
			if _, ok := p.resyncConfigFileCache.Load(configKey); ok != tc.wantCached {
				t.Fatalf("ConfigMap cache=%v want=%v", ok, tc.wantCached)
			}
		})
	}
}
