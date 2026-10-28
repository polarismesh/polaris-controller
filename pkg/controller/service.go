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
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"

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
		log.SyncNamingScope().Errorf("Error get update services old %v, new %v", old, current)
		return
	}

	task := &Task{
		Namespace:  curService.GetNamespace(),
		Name:       curService.GetName(),
		ObjectType: KubernetesService,
	}

	// 这里需要确认是否加入 Service 进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := p.IsPolarisService(oldService)
	curIsPolaris := p.IsPolarisService(curService)
	if !oldIsPolaris && !curIsPolaris {
		return
	}

	// 现在已经不是需要同步的北极星服务
	if !curIsPolaris {
		task.Operation = OperationUpdate
		p.enqueueService(task, oldService, "Update")
		p.resyncServiceCache.Delete(task.Key())
		return
	}

	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		task.Operation = OperationUpdate
		p.enqueueService(task, oldService, "Update")
		p.serviceCache.Store(task.Key(), oldService)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		task.Operation = OperationAdd
		p.enqueueService(task, curService, "Add")
	}
	p.resyncServiceCache.Store(task.Key(), curService)
}

// onServiceAdd 在识别到是这个北极星类型service的时候进行处理
func (p *PolarisController) onServiceAdd(obj interface{}) {
	service, ok := obj.(*v1.Service)
	if !ok {
		return
	}

	if !p.IsPolarisService(service) {
		return
	}

	task := &Task{
		Namespace:  service.GetNamespace(),
		Name:       service.GetName(),
		ObjectType: KubernetesService,
		Operation:  OperationAdd,
	}

	p.enqueueService(task, service, "Add")
	p.resyncServiceCache.Store(task.Key(), service)
}

// onServiceDelete 在识别到是这个北极星类型service删除的时候，才进行处理
// 判断这个是否是北极星service，如果是北极星service，就加入队列。
func (p *PolarisController) onServiceDelete(obj interface{}) {
	service, ok := obj.(*v1.Service)
	if !ok {
		tombstone, isTombstone := obj.(cache.DeletedFinalStateUnknown)
		if !isTombstone {
			return
		}
		service, ok = tombstone.Obj.(*v1.Service)
		if !ok {
			return
		}
	}

	if !p.IsPolarisService(service) {
		return
	}

	task := &Task{
		Namespace:  service.GetNamespace(),
		Name:       service.GetName(),
		ObjectType: KubernetesService,
		Operation:  OperationDelete,
	}

	p.enqueueService(task, service, "Delete")
	p.resyncServiceCache.Delete(task.Key())
}

func (p *PolarisController) enqueueService(task *Task, service *v1.Service, eventType string) {
	metrics.SyncTimes.WithLabelValues(eventType, "Service").Inc()
	p.insertTask(task)
}

func (p *PolarisController) syncService(task *Task) error {
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("finished syncing service %q. (%v)", task.String(), time.Since(startTime))
	}()

	namespaces := task.Namespace
	name := task.Name

	service, err := p.serviceLister.Services(namespaces).Get(name)
	switch {
	case k8serrors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedService, ok := p.serviceCache.Load(task.Key())
		if !ok {
			if p.syncStateStore == nil {
				log.SyncNamingScope().Infof("Service %s not in cache. Ignoring the deletion", task.Key())
				return nil
			}
			records, stateErr := p.syncStateStore.MarkPendingForSource(context.Background(),
				p.config.PolarisController.ClusterName, task.Namespace, task.Name)
			if stateErr != nil {
				return stateErr
			}
			for _, record := range records {
				p.insertTask(record.sourceTask())
			}
			return nil
		}
		processError := p.deleteServiceWithState(cachedService)
		if processError != nil {
			// 由 handleTask 统一退避重试，保留缓存直到删除成功。
			p.eventRecorder.Eventf(cachedService, v1.EventTypeWarning, polarisEvent, "Delete polaris instances failed %v",
				processError)
			return processError
		}
		p.serviceCache.Delete(task.Key())
	case err != nil:
		log.SyncNamingScope().Errorf("Unable to retrieve service %v from store: %v", task.Key(), err)
		return err
	default:
		// 条件判断将会增加
		// 1. 首次创建service
		// 2. 更新service
		//    a. 北极星相关信息不变，更新了对应的metadata,ttl,权重信息
		//    b. 北极星相关信息改变，相当于删除旧的北极星信息，注册新的北极星信息。
		log.SyncNamingScope().Infof("Begin to process service %s", task.Key())

		// Namespace 任务同样根据当前对象和 Namespace 注解判断，无需补写 sync 注解。
		operationService := service

		cachedService, ok := p.serviceCache.Load(task.Key())
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			log.SyncNamingScope().Infof("Service %s not in cache, begin to add new polaris", task.Key())

			// 同步 k8s 的 namespace 和 service 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndService(operationService); processError != nil {
				log.SyncNamingScope().Errorf("ns/svc %s sync failed", task.Key())
				return processError
			}

			if !p.IsPolarisService(operationService) {
				// 不合法的 service ，只把 service 同步到北极星，不做后续的实例处理
				log.SyncNamingScope().Infof("service %s is not valid, do not process instance", task.Key())
				return nil
			}

			// 实例批量写入可能部分成功；保留归属以便失败重试期间删除 Service 时清理。
			p.serviceCache.Store(task.Key(), service)
			if processError := p.processSyncInstance(operationService); processError != nil {
				log.SyncNamingScope().Errorf("Service %s first add failed: %v", task.Key(), processError)
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateService(cachedService, operationService); processError != nil {
				log.SyncNamingScope().Errorf("Service %s update add failed: %v", task.Key(), processError)
				return processError
			}
		}
		p.serviceCache.Store(task.Key(), service)
	}
	return nil
}

func (p *PolarisController) processDeleteService(service *v1.Service) (err error) {
	log.SyncNamingScope().Infof("Delete Service %s/%s", service.GetNamespace(), service.GetName())
	instances, err := p.getAllInstance(service)
	if err != nil {
		return err
	}
	// 筛选出来只属于这个service的IP
	// 更简单的做法，直接从cachedEndpoint删除，处理简单
	// 通过查询北极星得到被注册的实例，直接删除，更加可靠。
	polarisIPs := p.filterPolarisMetadata(service, instances)

	log.SyncNamingScope().Infof("deRegistered [%s/%s] IPs , FilterPolarisMetadata \n %v \n %v",
		service.GetNamespace(), service.GetName(),
		instances, polarisIPs)

	// 平台接口
	return p.deleteInstances(service, polarisIPs)
}

// processSyncNamespaceAndService 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndService(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.SyncNamingScope().Infof("Begin to sync namespaces and service, %s", serviceMsg)

	if !p.IsPolarisService(service) {
		return nil
	}
	if err := p.ensureActiveSyncState(service); err != nil {
		return fmt.Errorf("persist active sync state for %s: %w", serviceMsg, err)
	}

	createNsResponse, err := polarisapi.CreateNamespaces(service.GetNamespace())
	if err != nil {
		log.SyncNamingScope().Errorf("Failed create namespaces in processSyncNamespaceAndService %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	createSvcResponse, err := polarisapi.CreateService(service)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		return err
	}

	if err := p.reconcileServiceAlias(service); err != nil {
		log.SyncNamingScope().Errorf("Failed to reconcile service alias %s: %v", serviceMsg, err)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, "AliasReconcileFailed",
			"Failed to reconcile service alias %s: %v", serviceMsg, err)
		return err
	}

	return nil
}

// processUpdateService 处理更新状态下的场景
func (p *PolarisController) processUpdateService(old, cur *v1.Service) error {
	// 更新分情况
	// 1. service 服务参数有变更[customWeight, ttl, autoRegister]
	//    * 同步该service
	//    * 更新serviceCache缓存
	// 2. service 无变更，那么可能只有endpoint变化
	//    * 仅需要同步对应的endpoint即可
	//    * 更新serviceCache缓存
	if cur.GetDeletionTimestamp() != nil {
		return p.deleteServiceWithState(cur)
	}
	if !p.IsPolarisService(cur) {
		return p.deleteServiceWithState(old)
	}
	if err := p.ensureActiveSyncState(cur); err != nil {
		serviceMsg := fmt.Sprintf("[%s,%s]", cur.Namespace, cur.Name)
		p.eventRecorder.Eventf(cur, v1.EventTypeWarning, "SyncStatePersistFailed",
			"Failed to persist sync state for %s: %v", serviceMsg, err)
		return fmt.Errorf("persist active sync state for %s: %w", serviceMsg, err)
	}
	aliasErr := p.reconcileServiceAlias(cur)
	if aliasErr != nil {
		serviceMsg := fmt.Sprintf("[%s,%s]", cur.Namespace, cur.Name)
		log.SyncNamingScope().Errorf("Failed to reconcile service alias %s: %v", serviceMsg, aliasErr)
		p.eventRecorder.Eventf(cur, v1.EventTypeWarning, "AliasReconcileFailed",
			"Failed to reconcile service alias %s: %v", serviceMsg, aliasErr)
	}

	k8sService := cur.GetNamespace() + "/" + cur.GetName()
	// 同步 service 的标签变化情况
	metadataErr := p.updateService(old, cur)
	if metadataErr != nil {
		log.SyncNamingScope().Error("process service update info", zap.String("service", k8sService), zap.Error(metadataErr))
	}

	changeType := p.CompareServiceChange(old, cur)
	var instanceErr error
	switch changeType {
	case util.WorkloadKind:
		log.SyncNamingScope().Infof("Service %s changed, need delete old and update", k8sService)
		syncErr := p.processSyncInstance(cur)
		deleteErr := p.processDeleteService(old)
		if syncErr != nil || deleteErr != nil {
			instanceErr = fmt.Errorf("failed service %s changed, need delete old and update new, %v|%v",
				k8sService, syncErr, deleteErr)
		}
	case util.InstanceMetadataChanged, util.InstanceTTLChanged,
		util.InstanceWeightChanged, util.InstanceCustomWeightChanged, util.InstanceEnableRegisterChanged:
		log.SyncNamingScope().Infof(
			"Service %s metadata,ttl,custom weight,enableRegister changed, need to update", k8sService)
		instanceErr = p.processSyncInstance(cur)
	default:
		log.SyncNamingScope().Infof("Service %s endpoints or ports changed", k8sService)
		instanceErr = p.processSyncInstance(cur)
	}
	return stderrors.Join(aliasErr, metadataErr, instanceErr)
}
