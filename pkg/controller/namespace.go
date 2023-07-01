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
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func (p *PolarisController) onNamespaceAdd(obj interface{}) {
	namespace := obj.(*v1.Namespace)
	if util.IgnoreObject(namespace) {
		return
	}
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		// 过滤掉不需要处理 ns
		if !util.EnableSync(namespace) {
			return
		}
	}

	p.enqueueNamespace(&Task{
		Namespace:  namespace.GetName(),
		Name:       namespace.GetName(),
		ObjectType: KubernetesNamespace,
	}, namespace)
}

func (p *PolarisController) onNamespaceUpdate(old, cur interface{}) {
	oldNs := old.(*v1.Namespace)
	curNs := cur.(*v1.Namespace)

	if util.IgnoreObject(oldNs) {
		return
	}

	isOldSync := p.IsNamespaceSyncEnable(oldNs)
	isCurSync := p.IsNamespaceSyncEnable(curNs)

	if !isOldSync && isCurSync {
		p.enqueueNamespace(&Task{
			Namespace:  curNs.GetName(),
			Name:       curNs.GetName(),
			ObjectType: KubernetesNamespace,
		}, curNs)
	}

	// 有几种情况：
	// 1. 无 sync -> 无 sync，不处理 ns 和 service
	// 2. 有 sync -> 有 sync，将 ns 下 service、configmap 加入队列，标志为 polaris 要处理的，即添加
	// 3. 无 sync -> 有 sync，将 ns 下 service、configmap 加入队列，标志为 polaris 要处理的，即添加
	// 4. 有 sync -> 无 sync，将 ns 下 service、configmap 加入队列，标志为 polaris 不需要处理的，即删除

	operation := OperationEmpty
	if !isOldSync && !isCurSync {
		// 情况 1
		return
	}
	if isCurSync {
		// 情况 2、3
		operation = OperationAdd
	} else {
		// 情况 4
		operation = OperationDelete
	}

	p.syncServiceOnNamespaceUpdate(oldNs, curNs, operation)
	p.syncConfigMapOnNamespaceUpdate(oldNs, curNs, operation)
}

func (p *PolarisController) syncNamespace(key string) error {
	log.Infof("Begin to sync namespaces %s", key)

	createNsResponse, err := polarisapi.CreateNamespaces(key)
	if err != nil {
		log.Errorf("Failed create namespaces in syncNamespace %s, err %s, resp %v", key, err, createNsResponse.Info)
	}
	return err
}

func (p *PolarisController) syncServiceOnNamespaceUpdate(oldNs, curNs *v1.Namespace, operation Operation) {
	services, err := p.serviceLister.Services(oldNs.Name).List(labels.Everything())
	if err != nil {
		log.SyncNamingScope().Errorf("get namespaces %s services error in onNamespaceUpdate, %v\n", curNs.Name, err)
		return
	}

	log.SyncNamingScope().Infof("namespace %s operation is %s", curNs.Name, operation)
	for _, service := range services {
		// 非法的 service 不处理
		if util.IgnoreService(service) {
			continue
		}
		// service 确定有 sync=true 标签的，不需要在这里投入队列。由后续 service 事件流程处理，减少一些冗余。
		// namespace 当前有 sync ，且 service sync 为 false 的场景，namespace 流程不处理， service 流程来处理。
		// 如果由 namespace 流程处理，则每次 resync ，多需要处理一次
		if p.IsPolarisService(service) || (operation == OperationAdd && !p.IsPolarisService(service)) {
			continue
		}

		task := &Task{
			Namespace:  service.GetNamespace(),
			Name:       service.GetName(),
			ObjectType: KubernetesService,
			Operation:  operation,
		}
		p.insertTask(task)
	}
}

func (p *PolarisController) syncConfigMapOnNamespaceUpdate(oldNs, curNs *v1.Namespace, operation Operation) {
	if !p.OpenSyncConfigMap() {
		return
	}

	configMaps, err := p.configMapLister.ConfigMaps(oldNs.Name).List(labels.Everything())
	if err != nil {
		log.SyncConfigScope().Errorf("get namespaces %s ConfigMaps error in onNamespaceUpdate, %v\n", curNs.Name, err)
		return
	}

	log.SyncConfigScope().Infof("namespace %s operation is %s", curNs.Name, operation)
	for _, configMap := range configMaps {
		if util.IgnoreObject(configMap) {
			continue
		}
		// ConfigMap 确定有 sync=true 标签的，不需要在这里投入队列。由后续 ConfigMap 事件流程处理。
		// namespace 当前有 sync ，且 ConfigMap sync 为 false 的场景，namespace 流程不处理， ConfigMap 流程来处理。
		if p.IsPolarisConfigMap(configMap) || (operation == OperationAdd && !p.IsPolarisConfigMap(configMap)) {
			continue
		}

		task := &Task{
			Namespace:  configMap.GetNamespace(),
			Name:       configMap.GetName(),
			ObjectType: KubernetesConfigMap,
			Operation:  operation,
		}
		p.insertTask(task)
	}
}

func (p *PolarisController) enqueueNamespace(key *Task, namespace *v1.Namespace) {
	p.queue.Add(key)
}
