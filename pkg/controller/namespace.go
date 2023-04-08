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
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func (p *PolarisController) onNamespaceAdd(obj interface{}) {
	namespace := obj.(*v1.Namespace)

	if !util.IgnoreNamespace(namespace) {
		log.Infof("%s in ignore namespaces", namespace.Name)
		return
	}

	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		// 过滤掉不需要处理 ns
		if !util.IsNamespaceSyncEnable(namespace) {
			return
		}
	}

	key, err := util.GenObjectQueueKey(namespace)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", namespace, err))
		return
	}

	p.enqueueNamespace(key, namespace)
}

func (p *PolarisController) onNamespaceUpdate(old, cur interface{}) {
	oldNs := old.(*v1.Namespace)
	curNs := cur.(*v1.Namespace)

	if !util.IgnoreNamespace(oldNs) {
		log.Infof("ignore namespaces %s", oldNs.Name)
		return
	}

	nsKey, err := util.GenObjectQueueKey(curNs)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", curNs, err))
		return
	}

	// 处理 namespace
	if !util.IsNamespaceSyncEnable(oldNs) && util.IsNamespaceSyncEnable(curNs) {
		p.enqueueNamespace(nsKey, curNs)
	}

	if p.config.PolarisController.SyncMode == util.SyncModeDemand {

		// 有几种情况：
		// 1. 无 sync -> 无 sync，不处理 ns 和 service
		// 2. 有 sync -> 有 sync，将 ns 下 service 加入队列，标志为 polaris 要处理的，即添加
		// 3. 无 sync -> 有 sync，将 ns 下 service 加入队列，标志为 polaris 要处理的，即添加
		// 4. 有 sync -> 无 sync，将 ns 下 service 加入队列，标志为 polaris 不需要处理的，即删除

		isOldSync := util.IsNamespaceSyncEnable(oldNs)
		isCurSync := util.IsNamespaceSyncEnable(curNs)

		operation := ""
		if !isOldSync && !isCurSync {
			// 情况 1
			return
		} else if isCurSync {
			// 情况 2、3
			operation = ServiceKeyFlagAdd
		} else {
			// 情况 4
			operation = ServiceKeyFlagDelete
		}

		services, err := p.serviceLister.Services(oldNs.Name).List(labels.Everything())
		if err != nil {
			log.Errorf("get namespaces %s services error in onNamespaceUpdate, %v\n", curNs.Name, err)
			return
		}

		log.Infof("namespace %s operation is %s", curNs.Name, operation)

		for _, service := range services {
			// 非法的 service 不处理
			if util.IgnoreService(service) {
				continue
			}
			// service 确定有 sync=true 标签的，不需要在这里投入队列。由后续 service 事件流程处理，减少一些冗余。
			if util.IsServiceSyncEnable(service) {
				log.Infof("service %s/%s is enabled", service.Namespace, service.Name)
				continue
			}

			// namespace 当前有 sync ，且 service sync 为 false 的场景，namespace 流程不处理， service 流程来处理。
			// 如果由 namespace 流程处理，则每次 resync ，多需要处理一次
			if operation == ServiceKeyFlagAdd && util.IsServiceSyncDisable(service) {
				continue
			}

			key, err := util.GenServiceQueueKeyWithFlag(service, operation)
			if err != nil {
				log.Errorf("get key from service [%s/%s] in onNamespaceUpdate error, %v\n", oldNs, service.Name, err)
				continue
			}
			p.queue.Add(key)
		}
	}
}

func (p *PolarisController) enqueueNamespace(key string, namespace *v1.Namespace) {
	p.queue.Add(key)
}

func (p *PolarisController) syncNamespace(key string) error {
	log.Infof("Begin to sync namespaces %s", key)

	createNsResponse, err := polarisapi.CreateNamespaces(key)
	if err != nil {
		log.Errorf("Failed create namespaces in syncNamespace %s, err %s, resp %v",
			key, err, createNsResponse)
		return err
	}

	return nil
}
