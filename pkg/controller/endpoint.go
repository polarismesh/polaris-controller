/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package controller

import (
	"fmt"
	"reflect"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
	v1 "k8s.io/api/core/v1"
)

func (p *PolarisController) onEndpointAdd(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoint := obj.(*v1.Endpoints)

	if !util.IgnoreEndpoint(endpoint) {
		log.Infof("Endpoint %s/%s in ignore namespaces", endpoint.Namespace, endpoint.Name)
		return
	}

	key, err := util.GenObjectQueueKey(endpoint)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error %v", endpoint.Namespace, endpoint.Name, err)
		return
	}

	// 如果是 demand 模式，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(endpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", endpoint.Namespace, endpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	p.enqueueEndpoint(key, endpoint, "Add")
}

func (p *PolarisController) onEndpointUpdate(old, cur interface{}) {
	// 先确认service是否是Polaris的，后再做比较，会提高效率。
	oldEndpoint, ok1 := old.(*v1.Endpoints)
	curEndpoint, ok2 := cur.(*v1.Endpoints)

	// 过滤非法的 endpoints
	if !util.IgnoreEndpoint(curEndpoint) {
		return
	}

	key, err := util.GenObjectQueueKey(old)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error, %v", oldEndpoint.Namespace, oldEndpoint.Name, err)
		return
	}

	// demand 模式下，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(curEndpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", curEndpoint.Namespace, curEndpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	// 这里有不严谨的情况， endpoint update 时的service有从
	// 1. polaris -> not polaris
	// 2. not polaris -> polaris
	// 3. 只变更 endpoint.subsets
	if ok1 && ok2 && !reflect.DeepEqual(oldEndpoint.Subsets, curEndpoint.Subsets) {
		metrics.SyncTimes.WithLabelValues("Update", "Endpoint").Inc()
		log.Infof("Endpoints %s is updating, in queue", key)
		p.queue.Add(key)
	}
}

func (p *PolarisController) onEndpointDelete(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoint := obj.(*v1.Endpoints)

	if !util.IgnoreEndpoint(endpoint) {
		log.Infof("Endpoint %s/%s in ignore namespaces", endpoint.Namespace, endpoint.Name)
		return
	}

	key, err := util.GenObjectQueueKey(endpoint)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error %v", endpoint.Namespace, endpoint.Name, err)
		return
	}

	// 如果是 demand 模式，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(endpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", endpoint.Namespace, endpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	p.enqueueEndpoint(key, endpoint, "Delete")
}

// isPolarisEndpoints endpoints 的 service 和 namespace 是否带有 sync 注解。
// 返回是否了注解，如果带了注解，会返回应该加入到处理队列中的 key。
// 这里有几种情况：
// 1. service sync = true ，要处理
// 2. service sync 为 false， 不处理
// 3. service sync 为空， namespace sync = true ，要处理
// 4. service sync 为空， namespace sync 为空或者 false ，不处理
func (p *PolarisController) isPolarisEndpoints(endpoint *v1.Endpoints) (bool, string, error) {
	// 先检查 endpoints 的 service 上是否有注解
	service, err := p.serviceLister.Services(endpoint.GetNamespace()).Get(endpoint.GetName())
	if err != nil {
		log.Errorf("Unable to find the service of the endpoint %s/%s, %v",
			endpoint.Namespace, endpoint.Name, err)
		return false, "", err
	}

	if util.IsServiceSyncEnable(service) {
		// 情况 1 ，要处理
		key, err := util.GenServiceQueueKey(service)
		if err != nil {
			log.Errorf("get service %s/%s key in enqueueEndpoint error, %v", service.Namespace, service.Name, err)
			return false, "", err
		}
		return true, key, nil
	} else {
		// 检查 service 是否有 false 注解
		if util.IsServiceSyncDisable(service) {
			// 情况 2 ，不处理
			return false, "", nil
		}
		// 再检查 namespace 上是否有注解
		namespace, err := p.namespaceLister.Get(service.Namespace)
		if err != nil {
			log.Errorf("Unable to find the namespace of the endpoint %s/%s, %v",
				endpoint.Namespace, endpoint.Name, err)
			return false, "", err
		}
		if util.IsNamespaceSyncEnable(namespace) {
			// 情况 3 ，要处理
			key, err := util.GenServiceQueueKeyWithFlag(service, ServiceKeyFlagAdd)
			if err != nil {
				log.Errorf("Unable to find the key of the service %s/%s, %v",
					service.Namespace, service.Name, err)
			}
			return true, key, nil
		}
	}

	return false, "", nil
}

func (p *PolarisController) enqueueEndpoint(key string, endpoint *v1.Endpoints, eventType string) {
	log.Infof("Endpoint %s is polaris, in queue", key)
	metrics.SyncTimes.WithLabelValues(eventType, "Endpoint").Inc()
	p.queue.Add(key)
}

// processSyncInstance 同步实例, 获取Endpoint和北极星数据做同步
func (p *PolarisController) processSyncInstance(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.Infof("Begin to sync instance %s", serviceMsg)

	endpoint, err := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
	if err != nil {
		log.Errorf("Get endpoint of service %s error %v, ignore", serviceMsg, err)
		return err
	}

	// 1. 先获取当前service Endpoint中的IP信息，IP:Port:Weight
	// 2. 获取北极星中注册的Service，经过过滤，获取对应的 IP:Port:Weight
	// 3. 对比，获取三个数组： 增加，更新，删除
	// 4. 发起增加，更新，删除操作。
	instances, err := p.getAllInstance(service)
	if err != nil {
		log.Errorf("Get service instances from polaris of service %s error %v ", serviceMsg, err)
		return err
	}
	ipPortMap := getCustomWeight(service, serviceMsg)
	specIPs := address.GetAddressMapFromEndpoints(service, endpoint, p.podLister, ipPortMap)
	currentIPs := address.GetAddressMapFromPolarisInstance(instances, p.config.PolarisController.ClusterName)
	addIns, deleteIns, updateIns := p.CompareInstance(service, specIPs, currentIPs)

	log.Infof("%s Current polaris instance from sdk is %v", serviceMsg, instances)
	log.Infof("%s Spec endpoint instance is %v", serviceMsg, specIPs)
	log.Infof("%s Current polaris instance is %v", serviceMsg, currentIPs)
	log.Infof("%s addIns %v deleteIns %v updateIns %v", serviceMsg, addIns, deleteIns, updateIns)

	var addInsErr, deleteInsErr, updateInsErr error

	enableRegister := service.GetAnnotations()[util.PolarisEnableRegister]
	enableSync := service.GetAnnotations()[util.PolarisSync]
	// 如果 enableRegister = true,那么自注册，平台不负责注册IP
	if enableRegister != noEnableRegister || enableSync != noEnableRegister {
		// 使用platform 接口
		if addInsErr = p.addInstances(service, addIns); addInsErr != nil {
			log.Errorf("Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
		}

		if updateInsErr = p.updateInstances(service, updateIns); updateInsErr != nil {
			log.Errorf("Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
		}
	}

	if deleteInsErr = p.deleteInstances(service, deleteIns); deleteInsErr != nil {
		log.Errorf("Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
	}

	if addInsErr != nil || deleteInsErr != nil || updateInsErr != nil {
		return fmt.Errorf("failed AddInstances %v DeleteInstances %v UpdateInstances %v", addInsErr, deleteInsErr,
			updateInsErr)
	}

	return nil
}
