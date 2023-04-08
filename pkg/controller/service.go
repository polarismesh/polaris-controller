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
	"reflect"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

// onServiceUpdate 比较是否有必要加入service队列
func (p *PolarisController) onServiceUpdate(old, current interface{}) {
	oldService, ok1 := old.(*v1.Service)
	curService, ok2 := current.(*v1.Service)

	if !(ok1 && ok2) {
		log.Errorf("Error get update services old %v, new %v", old, current)
		return
	}

	key, err := util.GenServiceQueueKey(curService)
	if err != nil {
		log.Errorf("generate service queue key in onServiceUpdate error, %v", err)
		return
	}

	log.Infof("Service %s/%s is update", curService.GetNamespace(), curService.GetName())

	// 如果是按需同步，则处理一下 sync 为 空 -> sync 为 false 的场景，需要删除。
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsServiceHasSyncAnnotation(oldService) && util.IsServiceSyncDisable(curService) {
			log.Infof("Service %s is update because sync no to false", key)
			metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
			p.queue.Add(key)
			p.resyncServiceCache.Delete(util.GenServiceResyncQueueKeyWithOrigin(key))
			return
		}
	}

	// 这里需要确认是否加入svc进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := util.IsPolarisService(oldService, p.config.PolarisController.SyncMode)
	curIsPolaris := util.IsPolarisService(curService, p.config.PolarisController.SyncMode)
	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		log.Infof("Service %s is update polaris config", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
		p.serviceCache.Store(key, oldService)
		p.resyncServiceCache.Store(util.GenServiceResyncQueueKeyWithOrigin(key), curService)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		log.Infof("Service %s is update to polaris type", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
		p.resyncServiceCache.Store(util.GenServiceResyncQueueKeyWithOrigin(key), curService)
	}
}

// onServiceAdd 在识别到是这个北极星类型service的时候进行处理
func (p *PolarisController) onServiceAdd(obj interface{}) {
	service := obj.(*v1.Service)

	if !util.IsPolarisService(service, p.config.PolarisController.SyncMode) {
		return
	}

	key, err := util.GenServiceQueueKey(service)
	if err != nil {
		log.Errorf("generate queue key for %s/%s error, %v", service.Namespace, service.Name, err)
		return
	}

	p.enqueueService(key, service, "Add")
	p.resyncServiceCache.Store(util.GenServiceResyncQueueKeyWithOrigin(key), service)
}

// onServiceDelete 在识别到是这个北极星类型service删除的时候，才进行处理
// 判断这个是否是北极星service，如果是北极星service，就加入队列。
func (p *PolarisController) onServiceDelete(obj interface{}) {
	service := obj.(*v1.Service)

	key, err := util.GenServiceQueueKey(service)
	if err != nil {
		log.Errorf("generate queue key for %s/%s error, %v", service.Namespace, service.Name, err)
		return
	}

	// demand 模式中。 删除服务时，如果 ns 的 sync 为 true ，则在 service 流程里补偿处理，因为 ns 流程感知不到 service 删除
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		ns, err := p.namespaceLister.Get(service.Namespace)
		if err != nil {
			log.Errorf("get namespace for service %s/%s error, %v", service.Namespace, service.Name, err)
			return
		}
		if util.IsNamespaceSyncEnable(ns) {
			log.Infof("Service %s is polaris type, in queue", key)
			metrics.SyncTimes.WithLabelValues("Delete", "Service").Inc()
			p.queue.Add(key)
		}
	}

	if !util.IsPolarisService(service, p.config.PolarisController.SyncMode) {
		return
	}

	p.enqueueService(key, service, "Delete")
	p.resyncServiceCache.Delete(util.GenServiceResyncQueueKeyWithOrigin(key))
}

func (p *PolarisController) enqueueService(key string, service *v1.Service, eventType string) {
	log.Infof("Service %s is polaris type, in queue", key)
	metrics.SyncTimes.WithLabelValues(eventType, "Service").Inc()
	p.queue.Add(key)
}

func (p *PolarisController) syncService(key string) error {
	log.Infof("Start sync service %s, queue deep %d", key, p.queue.Len())
	startTime := time.Now()
	defer func() {
		log.Infof("Finished syncing service %q service. (%v)", key, time.Since(startTime))
	}()

	realKey, namespaces, name, op, err := util.GetServiceRealKeyWithFlag(key)
	if err != nil {
		log.Errorf("GetServiceRealKeyWithFlag %s error, %v", key, err)
		return err
	}

	service, err := p.serviceLister.Services(namespaces).Get(name)
	switch {
	case errors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedService, ok := p.serviceCache.Load(realKey)
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			log.Errorf("Service %s not in cache even though the watcher thought it was. Ignoring the deletion", realKey)
			return nil
		}
		log.Infof("Service %s is in cache, cache info %v", realKey, cachedService.Name)
		// 查询原svc是啥，删除对应实例
		processError := p.processDeleteService(cachedService)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedService, v1.EventTypeWarning, polarisEvent, "Delete polaris instances failed %v",
				processError)
			return processError
		}
		p.serviceCache.Delete(realKey)
	case err != nil:
		log.Errorf("Unable to retrieve service %v from store: %v", realKey, err)
	default:
		// 条件判断将会增加
		// 1. 首次创建service
		// 2. 更新service
		//    a. 北极星相关信息不变，更新了对应的metadata,ttl,权重信息
		//    b. 北极星相关信息改变，相当于删除旧的北极星信息，注册新的北极星信息。
		log.Infof("Begin to process service %s, operation is %s", realKey, op)

		operationService := service

		// 如果是 namespace 流程传来的任务，且 op 为 add ，为 service 打上 sync 标签
		if op == ServiceKeyFlagAdd && !util.IsServiceSyncDisable(operationService) {
			operationService = service.DeepCopy()
			v12.SetMetaDataAnnotation(&operationService.ObjectMeta, util.PolarisSync, util.IsEnableSync)
		}

		cachedService, ok := p.serviceCache.Load(realKey)
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			log.Infof("Service %s not in cache, begin to add new polaris", realKey)

			// 同步 k8s 的 namespace 和 service 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndService(operationService); processError != nil {
				log.Errorf("ns/svc %s sync failed", realKey)
				return processError
			}

			if !util.IsPolarisService(operationService, p.config.PolarisController.SyncMode) {
				// 不合法的 service ，只把 service 同步到北极星，不做后续的实例处理
				log.Infof("service %s is not valid, do not process ", realKey)
				return nil
			}

			if processError := p.processSyncInstance(operationService); processError != nil {
				log.Errorf("Service %s first add failed", realKey)
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateService(cachedService, operationService); processError != nil {
				log.Errorf("Service %s update add failed", realKey)
				return processError
			}
		}
		p.serviceCache.Store(realKey, service)
	}
	return nil
}

func (p *PolarisController) processDeleteService(service *v1.Service) (err error) {
	log.Infof("Delete Service %s/%s", service.GetNamespace(), service.GetName())

	instances, err := p.getAllInstance(service)
	if err != nil {
		return err
	}
	// 筛选出来只属于这个service的IP
	// 更简单的做法，直接从cachedEndpoint删除，处理简单
	// 通过查询北极星得到被注册的实例，直接删除，更加可靠。
	polarisIPs := p.filterPolarisMetadata(service, instances)

	log.Infof("deRegistered [%s/%s] IPs , FilterPolarisMetadata \n %v \n %v",
		service.GetNamespace(), service.GetName(),
		instances, polarisIPs)

	// 平台接口
	return p.deleteInstances(service, polarisIPs)
}

// processSyncNamespaceAndService 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndService(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.Infof("Begin to sync namespaces and service, %s", serviceMsg)

	// demand 模式，service 不包含 sync 注解时，不需要创建 ns、service 和 alias
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsServiceSyncEnable(service) {
			return nil
		}
	}

	// service 包含sync注解，但是sync注解为false, 不需要创建对应的service
	if util.IsServiceSyncDisable(service) {
		return nil
	}

	createNsResponse, err := polarisapi.CreateNamespaces(service.Namespace)
	if err != nil {
		log.Errorf("Failed create namespaces in processSyncNamespaceAndService %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	createSvcResponse, err := polarisapi.CreateService(service)
	if err != nil {
		log.Errorf("Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		return err
	}

	if service.Annotations[util.PolarisAliasNamespace] != "" &&
		service.Annotations[util.PolarisAliasService] != "" {
		createAliasResponse, err := polarisapi.CreateServiceAlias(service)
		if err != nil {
			log.Errorf("Failed create service alias in processSyncNamespaceAndService %s, err %s, resp %v",
				serviceMsg, err, createAliasResponse)
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed create service alias %s, err %s, resp %v", serviceMsg, err, createAliasResponse)
			return err
		}
	}

	return nil
}

// processUpdateService 处理更新状态下的场景
func (p *PolarisController) processUpdateService(old, cur *v1.Service) (err error) {
	// 更新分情况
	// 1. service 服务参数有变更[customWeight, ttl, autoRegister]
	//    * 同步该service
	//    * 更新serviceCache缓存
	// 2. service 无变更，那么可能只有endpoint变化
	//    * 仅需要同步对应的endpoint即可
	//    * 更新serviceCache缓存
	if (p.config.PolarisController.SyncMode != util.SyncModeDemand && !util.IsServiceSyncDisable(cur) ||
		p.config.PolarisController.SyncMode == util.SyncModeDemand && util.IsServiceSyncEnable(cur)) &&
		util.IfNeedCreateServiceAlias(old, cur) {
		createAliasResponse, err := polarisapi.CreateServiceAlias(cur)
		if err != nil {
			serviceMsg := fmt.Sprintf("[%s,%s]", cur.Namespace, cur.Name)
			log.Errorf("Failed create service alias in processUpdateService %s, err %s, resp %v",
				serviceMsg, err, createAliasResponse)
			p.eventRecorder.Eventf(cur, v1.EventTypeWarning, polarisEvent,
				"Failed create service alias %s, err %s, resp %v", serviceMsg, err, createAliasResponse)
		}
	}

	k8sService := cur.GetNamespace() + "/" + cur.GetName()
	if !reflect.DeepEqual(old.Labels, cur.Labels) {
		// 同步 service 的标签变化情况
		if err := p.updateService(cur); err != nil {
			log.Error("process service update info", zap.String("service", k8sService), zap.Error(err))
		}
	}

	changeType := util.CompareServiceChange(old, cur, p.config.PolarisController.SyncMode)
	switch changeType {
	case util.ServicePolarisDelete:
		log.Infof("Service %s %s, need delete ", k8sService, util.ServicePolarisDelete)
		return p.processDeleteService(old)
	case util.WorkloadKind:
		log.Infof("Service %s changed, need delete old and update", k8sService)
		syncErr := p.processSyncInstance(cur)
		deleteErr := p.processDeleteService(old)
		if syncErr != nil || deleteErr != nil {
			return fmt.Errorf("failed service %s changed, need delete old and update new, %v|%v",
				k8sService, syncErr, deleteErr)
		}
		return
	case util.InstanceMetadataChanged, util.InstanceTTLChanged,
		util.InstanceWeightChanged, util.InstanceCustomWeightChanged:
		log.Infof("Service %s metadata,ttl,custom weight changed, need to update", k8sService)
		return p.processSyncInstance(cur)
	case util.InstanceEnableRegisterChanged:
		log.Infof("Service %s enableRegister,service weight changed, do nothing", k8sService)
		return
	default:
		log.Infof("Service %s endpoints or ports changed", k8sService)
		return p.processSyncInstance(cur)
	}
}
