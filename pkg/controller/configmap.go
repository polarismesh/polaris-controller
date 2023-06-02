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
	"time"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (p *PolarisController) enqueueConfigMap(key string, configmap *v1.ConfigMap, eventType string) {
	log.Infof("ConfigMap %s is polaris type, in queue", key)
	metrics.SyncTimes.WithLabelValues(eventType, "ConfigMap").Inc()
	p.queue.Add(key)
}

func (p *PolarisController) syncConfigMap(key string) error {
	log.Infof("Start sync ConfigMap %s, queue deep %d", key, p.queue.Len())
	startTime := time.Now()
	defer func() {
		log.Infof("Finished syncing ConfigMap %q. (%v)", key, time.Since(startTime))
	}()

	realKey, namespaces, name, op, err := util.GetResourceRealKeyWithFlag(key)
	if err != nil {
		log.Errorf("GetResourceRealKeyWithFlag %s error, %v", key, err)
		return err
	}

	configMap, err := p.configMapLister.ConfigMaps(namespaces).Get(name)
	switch {
	case errors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedConfigMap, ok := p.configFileCache.Load(realKey)
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			log.Errorf("ConfigMap %s not in cache even though the watcher thought it was. Ignoring the deletion", realKey)
			return nil
		}
		log.Infof("ConfigMap %s is in cache, cache info %v", realKey, cachedConfigMap.Name)
		// 查询原 ConfigMap 是啥，删除对应实例
		processError := p.processDeleteConfigMap(cachedConfigMap)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedConfigMap, v1.EventTypeWarning, polarisEvent, "Delete polaris ConfigMap failed %v",
				processError)
			p.enqueueConfigMap(key, cachedConfigMap, op)
			return processError
		}
		p.configFileCache.Delete(realKey)
	case err != nil:
		log.Errorf("Unable to retrieve ConfigMap %v from store: %v", realKey, err)
		p.enqueueConfigMap(key, nil, op)
	default:
		// 条件判断将会增加
		// 1. 首次创建 ConfigMap
		// 2. 更新 ConfigMap
		log.Infof("Begin to process ConfigMap %s, operation is %s", realKey, op)

		operationConfigMap := configMap

		// 如果是 namespace 流程传来的任务，且 op 为 add ，为 ConfigMap 打上 sync 标签
		if op == ServiceKeyFlagAdd && !util.IsConfigMapSyncDisable(operationConfigMap) {
			operationConfigMap = configMap.DeepCopy()
			metav1.SetMetaDataAnnotation(&operationConfigMap.ObjectMeta, util.PolarisSync, util.IsEnableSync)
		}

		cachedConfigMap, ok := p.configFileCache.Load(realKey)
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			log.Infof("ConfigMap %s not in cache, begin to add new polaris", realKey)
			if !util.IsPolarisConfigMap(operationConfigMap, p.config.PolarisController.SyncMode) {
				log.Infof("ConfigMap %s is not valid, do not process ", realKey)
				return nil
			}
			// 同步 k8s 的 namespace 和 service 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndConfigMap(operationConfigMap); processError != nil {
				log.Errorf("ns/config %s sync failed", realKey)
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateConfigMap(cachedConfigMap, operationConfigMap); processError != nil {
				log.Errorf("ConfigMap %s update add failed", realKey)
				return processError
			}
		}
		p.configFileCache.Store(realKey, configMap)
	}
	return nil
}

// processSyncNamespaceAndConfigMap 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndConfigMap(configmap *v1.ConfigMap) error {
	serviceMsg := fmt.Sprintf("[%s/%s]", configmap.GetNamespace(), configmap.GetName())
	log.Infof("Begin to sync namespaces and ConfigMap, %s", serviceMsg)

	// demand 模式，ConfigMap 不包含 sync 注解时，不需要创建 ns、ConfigMap
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsConfigMapSyncEnable(configmap) {
			return nil
		}
	}

	// ConfigMap 包含sync注解，但是sync注解为false, 不需要创建对应的 ConfigMap
	if util.IsConfigMapSyncDisable(configmap) {
		return nil
	}

	createNsResponse, err := polarisapi.CreateNamespaces(configmap.Namespace)
	if err != nil {
		log.Errorf("Failed create namespaces in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	createFileResp, err := polarisapi.CreateConfigMap(configmap)
	if err != nil {
		log.Errorf("Failed create ConfigMap in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createFileResp)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create ConfigMap %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}
	return nil
}

// processUpdateConfigMap 处理更新状态下的场景
func (p *PolarisController) processUpdateConfigMap(old, cur *v1.ConfigMap) error {
	log.Infof("Update ConfigMap %s/%s", cur.GetNamespace(), cur.GetName())

	if !util.IsPolarisConfigMap(cur, p.config.PolarisController.SyncMode) {
		return polarisapi.DeleteConfigMap(cur)
	}
	_, err := polarisapi.UpdateConfigMap(cur)
	return err
}

func (p *PolarisController) processDeleteConfigMap(configmap *v1.ConfigMap) error {
	log.Infof("Delete ConfigMap %s/%s", configmap.GetNamespace(), configmap.GetName())
	// 平台接口
	return polarisapi.DeleteConfigMap(configmap)
}

func (p *PolarisController) onConfigMapAdd(cur interface{}) {
	configMap := cur.(*v1.ConfigMap)

	if !util.IsPolarisConfigMap(configMap, p.config.PolarisController.SyncMode) {
		return
	}

	key, err := util.GenConfigMapQueueKey(configMap)
	if err != nil {
		log.Errorf("generate queue key for configmap %s/%s error, %v", configMap.Namespace, configMap.Name, err)
		return
	}

	p.enqueueConfigMap(key, configMap, "Add")
	p.resyncConfigFileCache.Store(util.GenResourceResyncQueueKeyWithOrigin(key), configMap)
}

func (p *PolarisController) onConfigMapUpdate(old, cur interface{}) {
	oldCm, ok1 := old.(*v1.ConfigMap)
	curCm, ok2 := cur.(*v1.ConfigMap)

	if !(ok1 && ok2) {
		log.Errorf("Error get update ConfigMaps old %v, new %v", old, cur)
		return
	}

	key, err := util.GenConfigMapQueueKey(curCm)
	if err != nil {
		log.Errorf("generate ConfigMap queue key in onConfigMapUpdate error, %v", err)
		return
	}

	log.Infof("ConfigMap %s/%s is update", curCm.GetNamespace(), curCm.GetName())

	// 如果是按需同步，则处理一下 sync 为 空 -> sync 为 false 的场景，需要删除。
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsConfigMapHasSyncAnnotation(oldCm) && util.IsConfigMapSyncDisable(curCm) {
			log.Infof("ConfigMap %s is update because sync no to false", key)
			metrics.SyncTimes.WithLabelValues("Update", "ConfigMap").Inc()
			p.queue.Add(key)
			p.resyncConfigFileCache.Delete(util.GenResourceResyncQueueKeyWithOrigin(key))
			return
		}
	}

	// 这里需要确认是否加入svc进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := util.IsPolarisConfigMap(oldCm, p.config.PolarisController.SyncMode)
	curIsPolaris := util.IsPolarisConfigMap(curCm, p.config.PolarisController.SyncMode)
	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		log.Infof("ConfigMap %s is update polaris config", key)
		metrics.SyncTimes.WithLabelValues("Update", "ConfigMap").Inc()
		p.queue.Add(key)
		p.configFileCache.Store(key, oldCm)
		p.resyncConfigFileCache.Store(util.GenResourceResyncQueueKeyWithOrigin(key), curCm)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		log.Infof("ConfigMap %s is update to polaris type", key)
		metrics.SyncTimes.WithLabelValues("Update", "ConfigMap").Inc()
		p.queue.Add(key)
		p.resyncConfigFileCache.Store(util.GenResourceResyncQueueKeyWithOrigin(key), curCm)
	}
}

func (p *PolarisController) onConfigMapDelete(cur interface{}) {
	configmap := cur.(*v1.ConfigMap)

	key, err := util.GenConfigMapQueueKey(configmap)
	if err != nil {
		log.Errorf("generate queue key for ConfigMap %s/%s error, %v", configmap.Namespace, configmap.Name, err)
		return
	}

	// demand 模式中。 删除服务时，如果 ns 的 sync 为 true ，则在 service 流程里补偿处理，因为 ns 流程感知不到 service 删除
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		ns, err := p.namespaceLister.Get(configmap.Namespace)
		if err != nil {
			log.Errorf("get namespace for ConfigMap %s/%s error, %v", configmap.Namespace, configmap.Name, err)
			return
		}
		if util.IsNamespaceSyncEnable(ns) {
			log.Infof("ConfigMap %s is polaris type, in queue", key)
			metrics.SyncTimes.WithLabelValues("Delete", "ConfigMap").Inc()
			p.queue.Add(key)
		}
	}

	if !util.IsPolarisConfigMap(configmap, p.config.PolarisController.SyncMode) {
		return
	}

	p.enqueueConfigMap(key, configmap, "Delete")
	p.resyncServiceCache.Delete(util.GenResourceResyncQueueKeyWithOrigin(key))
}
