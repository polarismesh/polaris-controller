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
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
						p.serviceCache.Store(task.Key(), svc)
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
