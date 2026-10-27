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

	if !isOldSync && !isCurSync {
		// 情况 1
		return
	}
	var operation Operation
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
		// 对象注解优先；关闭 Namespace 同步不能删除显式开启同步的 Service。
		if sync, ok := service.Annotations[util.PolarisSync]; ok &&
			(operation == OperationDelete || sync != util.IsEnableSync) {
			continue
		}

		task := &Task{
			Namespace:  service.GetNamespace(),
			Name:       service.GetName(),
			ObjectType: KubernetesService,
			Operation:  operation,
		}
		if operation == OperationAdd {
			p.resyncServiceCache.Store(task.Key(), service)
		} else {
			p.resyncServiceCache.Delete(task.Key())
		}
		p.insertTask(task)
	}
}

func (p *PolarisController) syncConfigMapOnNamespaceUpdate(oldNs, curNs *v1.Namespace, operation Operation) {
	if !p.AllowSyncFromConfigMap() {
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
		if configMap.Annotations[util.InternalConfigFileSyncSourceKey] == util.SourceFromPolaris {
			continue
		}
		if sync, ok := configMap.Annotations[util.PolarisSync]; ok &&
			(operation == OperationDelete || sync != util.IsEnableSync) {
			continue
		}

		task := &Task{
			Namespace:  configMap.GetNamespace(),
			Name:       configMap.GetName(),
			ObjectType: KubernetesConfigMap,
			Operation:  operation,
		}
		if operation == OperationAdd {
			p.resyncConfigFileCache.Store(task.Key(), configMap)
		} else {
			p.resyncConfigFileCache.Delete(task.Key())
		}
		p.insertTask(task)
	}
}

func (p *PolarisController) enqueueNamespace(key *Task, namespace *v1.Namespace) {
	p.insertTask(key)
}
