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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func (p *PolarisController) enqueueConfigMap(task *Task, configmap *v1.ConfigMap, eventType string) {
	metrics.SyncTimes.WithLabelValues(eventType, "ConfigMap").Inc()
	p.insertTask(task)
}

func (p *PolarisController) syncConfigMap(task *Task) error {
	startTime := time.Now()
	defer func() {
		log.SyncConfigScope().Infof("Finished syncing ConfigMap %v. (%v)", task, time.Since(startTime))
	}()

	namespaces := task.Namespace
	name := task.Name
	op := task.Operation

	configMap, err := p.configMapLister.ConfigMaps(namespaces).Get(name)
	switch {
	case errors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedConfigMap, ok := p.configFileCache.Load(task.Key())
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			log.SyncConfigScope().Infof("ConfigMap %s not in cache. Ignoring the deletion", task.Key())
			return nil
		}
		// 查询原 ConfigMap 是啥，删除对应实例
		processError := p.processDeleteConfigMap(cachedConfigMap)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedConfigMap, v1.EventTypeWarning, polarisEvent, "Delete polaris ConfigMap failed %v",
				processError)
			p.enqueueConfigMap(task, cachedConfigMap, string(op))
			return processError
		}
		p.configFileCache.Delete(task.Key())
	case err != nil:
		log.SyncConfigScope().Errorf("Unable to retrieve ConfigMap %v from store: %v", task.Key(), err)
		p.enqueueConfigMap(task, nil, string(op))
	default:
		// 条件判断将会增加
		// 1. 首次创建 ConfigMap
		// 2. 更新 ConfigMap
		operationConfigMap := configMap

		// 如果是 namespace 流程传来的任务，且 op 为 add ，为 ConfigMap 打上 sync 标签
		if op == ResourceKeyFlagAdd && p.IsPolarisConfigMap(operationConfigMap) {
			operationConfigMap = configMap.DeepCopy()
			metav1.SetMetaDataAnnotation(&operationConfigMap.ObjectMeta, util.PolarisSync, util.IsEnableSync)
		}

		cachedConfigMap, ok := p.configFileCache.Load(task.Key())
		if !ok {
			if !p.IsPolarisConfigMap(operationConfigMap) {
				return nil
			}
			// 1. cached中没有数据，为首次添加场景
			log.SyncConfigScope().Infof("ConfigMap %s not in cache, begin to add new polaris", task.Key())
			// 同步 k8s 的 namespace 和 configmap 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndConfigMap(operationConfigMap); processError != nil {
				log.SyncConfigScope().Errorf("ns/config %s sync failed", task.Key())
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateConfigMap(cachedConfigMap, operationConfigMap); processError != nil {
				log.SyncConfigScope().Errorf("ConfigMap %s update add failed", task.Key())
				return processError
			}
		}
		p.configFileCache.Store(task.Key(), configMap)
	}
	return nil
}

// processSyncNamespaceAndConfigMap 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndConfigMap(configmap *v1.ConfigMap) error {
	serviceMsg := fmt.Sprintf("[%s/%s]", configmap.GetNamespace(), configmap.GetName())

	// ConfigMap 包含sync注解，但是sync注解为false, 不需要创建对应的 ConfigMap
	if !p.IsPolarisConfigMap(configmap) {
		return nil
	}

	createNsResponse, err := polarisapi.CreateNamespaces(configmap.Namespace)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed create namespaces in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	log.SyncConfigScope().Infof("Create ConfigMap %s/%s", configmap.GetNamespace(), configmap.GetName())
	createFileResp, err := polarisapi.CreateConfigMap(configmap)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed create ConfigMap in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createFileResp)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create ConfigMap %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}
	return nil
}

// processUpdateConfigMap 处理更新状态下的场景
func (p *PolarisController) processUpdateConfigMap(old, cur *v1.ConfigMap) error {
	log.SyncConfigScope().Infof("Update ConfigMap %s/%s", cur.GetNamespace(), cur.GetName())
	if !p.IsPolarisConfigMap(cur) {
		return polarisapi.DeleteConfigMap(cur)
	}
	_, err := polarisapi.UpdateConfigMap(cur)
	return err
}

func (p *PolarisController) processDeleteConfigMap(configmap *v1.ConfigMap) error {
	log.SyncConfigScope().Infof("Delete ConfigMap %s/%s", configmap.GetNamespace(), configmap.GetName())
	// 平台接口
	return polarisapi.DeleteConfigMap(configmap)
}

func (p *PolarisController) onConfigMapAdd(cur interface{}) {
	configMap, ok := cur.(*v1.ConfigMap)
	if !ok {
		return
	}

	if !p.IsPolarisConfigMap(configMap) {
		return
	}

	task := &Task{
		Namespace:  configMap.GetNamespace(),
		Name:       configMap.GetName(),
		ObjectType: KubernetesConfigMap,
	}

	p.enqueueConfigMap(task, configMap, "Add")
	p.resyncConfigFileCache.Store(task.Key(), configMap)
}

func (p *PolarisController) onConfigMapUpdate(old, cur interface{}) {
	oldCm, ok1 := old.(*v1.ConfigMap)
	curCm, ok2 := cur.(*v1.ConfigMap)

	if !(ok1 && ok2) {
		log.SyncConfigScope().Errorf("Error get update ConfigMaps old %v, new %v", old, cur)
		return
	}

	task := &Task{
		Namespace:  curCm.GetNamespace(),
		Name:       curCm.GetName(),
		ObjectType: KubernetesConfigMap,
	}

	// 这里需要确认是否加入svc进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := p.IsPolarisConfigMap(oldCm)
	curIsPolaris := p.IsPolarisConfigMap(curCm)

	// 现在已经不是需要同步的北极星配置
	if !curIsPolaris {
		p.enqueueConfigMap(task, oldCm, "Update")
		p.resyncConfigFileCache.Delete(task.Key())
		return
	}

	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		p.enqueueConfigMap(task, oldCm, "Update")
		p.configFileCache.Store(task.Key(), oldCm)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		p.enqueueConfigMap(task, curCm, "Update")
	}
	p.resyncConfigFileCache.Store(task.Key(), curCm)
}

func (p *PolarisController) onConfigMapDelete(cur interface{}) {
	configmap, ok := cur.(*v1.ConfigMap)
	if !ok {
		return
	}

	task := &Task{
		Namespace:  configmap.GetNamespace(),
		Name:       configmap.GetName(),
		ObjectType: KubernetesConfigMap,
	}

	if !p.IsPolarisConfigMap(configmap) {
		return
	}

	p.enqueueConfigMap(task, configmap, "Delete")
	p.resyncConfigFileCache.Delete(task.Key())
}
