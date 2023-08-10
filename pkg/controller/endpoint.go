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
	"strings"

	v1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

func (p *PolarisController) onEndpointAdd(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoints := obj.(*v1.Endpoints)

	if util.IgnoreObject(endpoints) {
		return
	}

	sync, key, err := p.isPolarisEndpoints(endpoints)
	if err != nil {
		log.SyncNamingScope().Errorf("check if ep %s/%s svc/ns has sync error, %v",
			endpoints.Namespace, endpoints.Name, err)
		return
	}
	// 没有注解，不处理
	if !sync {
		return
	}
	p.enqueueEndpoint(key, endpoints, "Add")
}

func (p *PolarisController) onEndpointUpdate(old, cur interface{}) {
	// 先确认service是否是Polaris的，后再做比较，会提高效率。
	oldEndpoints, ok1 := old.(*v1.Endpoints)
	curEndpoints, ok2 := cur.(*v1.Endpoints)

	// 过滤非法的 endpoints
	if util.IgnoreObject(curEndpoints) {
		return
	}

	sync, task, err := p.isPolarisEndpoints(curEndpoints)
	if err != nil {
		log.SyncNamingScope().Errorf("check if ep %s/%s svc/ns has sync error, %v",
			curEndpoints.Namespace, curEndpoints.Name, err)
		return
	}
	// 没有注解，不处理
	if !sync {
		return
	}

	// 这里有不严谨的情况， endpoint update 时的service有从
	// 1. polaris -> not polaris
	// 2. not polaris -> polaris
	// 3. 只变更 endpoint.subsets
	if ok1 && ok2 && !reflect.DeepEqual(oldEndpoints.Subsets, curEndpoints.Subsets) {
		metrics.SyncTimes.WithLabelValues("Update", "Endpoint").Inc()
		log.SyncNamingScope().Infof("Endpoints %s is updating, in queue", task.Key())
		p.queue.Add(task)
	}
}

func (p *PolarisController) onEndpointDelete(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoints := obj.(*v1.Endpoints)
	if util.IgnoreObject(endpoints) {
		return
	}

	sync, task, err := p.isPolarisEndpoints(endpoints)
	if err != nil {
		log.SyncNamingScope().Errorf("check if ep %s/%s svc/ns has sync error, %v",
			endpoints.Namespace, endpoints.Name, err)
		return
	}
	// 没有注解，不处理
	if !sync {
		return
	}

	p.enqueueEndpoint(task, endpoints, "Delete")
}

// isPolarisEndpoints endpoints 的 service 和 namespace 是否带有 sync 注解。
// 返回是否了注解，如果带了注解，会返回应该加入到处理队列中的 key。
// 这里有几种情况：
// 1. service sync = true ，要处理
// 2. service sync 为 false， 不处理
// 3. service sync 为空， namespace sync = true ，要处理
// 4. service sync 为空， namespace sync 为空或者 false ，不处理
func (p *PolarisController) isPolarisEndpoints(endpoints *v1.Endpoints) (bool, *Task, error) {
	if p.SyncMode() == util.SyncModeAll {
		return true, &Task{
			Namespace:  endpoints.GetNamespace(),
			Name:       endpoints.GetName(),
			ObjectType: KubernetesService,
		}, nil
	}

	// 先检查 endpoints 的 service 上是否有注解
	service, err := p.serviceLister.Services(endpoints.GetNamespace()).Get(endpoints.GetName())
	if err != nil {
		log.SyncNamingScope().Errorf("Unable to find the service of the endpoint %s/%s, %v",
			endpoints.Namespace, endpoints.Name, err)
		return false, nil, err
	}

	if isSync := p.IsPolarisService(service); !isSync {
		return false, nil, nil
	}

	if util.EnableSync(service) {
		return true, &Task{
			Namespace:  service.GetNamespace(),
			Name:       service.GetName(),
			ObjectType: KubernetesService,
		}, nil
	}
	// 再检查 namespace 上是否有注解
	namespace, err := p.namespaceLister.Get(service.Namespace)
	if err != nil {
		log.SyncNamingScope().Errorf("Unable to find the namespace of the endpoint %s/%s, %v",
			endpoints.Namespace, endpoints.Name, err)
		return false, nil, err
	}
	if util.EnableSync(namespace) {
		// 情况 3 ，要处理
		return true, &Task{
			Namespace:  service.GetNamespace(),
			Name:       service.GetName(),
			ObjectType: KubernetesService,
			Operation:  OperationAdd,
		}, nil
	}
	return false, nil, nil
}

func (p *PolarisController) enqueueEndpoint(task *Task, endpoint *v1.Endpoints, eventType string) {
	metrics.SyncTimes.WithLabelValues(eventType, "Endpoint").Inc()
	p.queue.Add(task)
}

// processSyncInstance 同步实例, 获取Endpoint和北极星数据做同步
func (p *PolarisController) processSyncInstance(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.SyncNamingScope().Infof("Begin to sync instance %s", serviceMsg)

	endpoints, err := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
	if err != nil {
		log.SyncNamingScope().Errorf("Get endpoint of service %s error %v, ignore", serviceMsg, err)
		return err
	}

	// 1. 先获取当前service Endpoint中的IP信息，IP:Port:Weight
	// 2. 获取北极星中注册的Service，经过过滤，获取对应的 IP:Port:Weight
	// 3. 对比，获取三个数组： 增加，更新，删除
	// 4. 发起增加，更新，删除操作。
	instances, err := p.getAllInstance(service)
	if err != nil {
		log.SyncNamingScope().Errorf("Get service instances from polaris of service %s error %v ", serviceMsg, err)
		return err
	}
	ipPortMap := getCustomWeight(service, serviceMsg)
	specIPs := address.GetAddressMapFromEndpoints(service, endpoints, p.podLister, ipPortMap)
	currentIPs := address.GetAddressMapFromPolarisInstance(instances, p.config.PolarisController.ClusterName)
	addIns, deleteIns, updateIns := p.CompareInstance(service, specIPs, currentIPs)

	msg := []string{
		fmt.Sprintf("%s Current polaris instance from sdk is %v", serviceMsg, instances),
		fmt.Sprintf("%s Spec endpoint instance is %v", serviceMsg, specIPs),
		fmt.Sprintf("%s Current polaris instance is %v", serviceMsg, currentIPs),
		fmt.Sprintf("%s addIns %v deleteIns %v updateIns %v", serviceMsg, addIns, deleteIns, updateIns),
	}
	log.SyncNamingScope().Infof(strings.Join(msg, "\n"))

	var addInsErr, deleteInsErr, updateInsErr error

	enableRegister := service.GetAnnotations()[util.PolarisEnableRegister]
	enableSync := service.GetAnnotations()[util.PolarisSync]
	// 如果 enableRegister = true,那么自注册，平台不负责注册IP
	if enableRegister != noAllow || enableSync != noAllow {
		// 使用platform 接口
		if addInsErr = p.addInstances(service, addIns); addInsErr != nil {
			log.SyncNamingScope().Errorf("Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
		}

		if updateInsErr = p.updateInstances(service, updateIns); updateInsErr != nil {
			log.SyncNamingScope().Errorf("Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
		}
	}

	if deleteInsErr = p.deleteInstances(service, deleteIns); deleteInsErr != nil {
		log.SyncNamingScope().Errorf("Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
	}
	return nil
}
