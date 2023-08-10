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

package address

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-go/pkg/model"
)

// Address 记录IP端口信息
type Address struct {
	IP              string
	Port            int
	PodName         string
	Index           string // stsp,sts才存在有序Pod
	Weight          int
	Healthy         bool
	Isolate         bool
	Metadata        map[string]string
	Protocol        string
	PolarisInstance model.Instance
}

// InstanceSet 实例信息表，以ip-port作为key
type InstanceSet map[string]*Address

// GetAddressMapFromEndpoints
//
//	形成
//	[{
//	"10.10.10.10-80": {
//	"ip": "10.10.10.10",
//	"port": "80"
//	 }
//	}]

func buildAddresses(endpoint *v1.EndpointAddress, subset *v1.EndpointSubset, hasIndex bool,
	podLister corelisters.PodLister, defaultWeight int, indexPortMap util.IndexPortMap, isolate bool) InstanceSet {
	tmpReadyIndex := ""
	if hasIndex {
		tmpIndexArray := strings.Split(endpoint.TargetRef.Name, "-")
		if len(tmpIndexArray) > 1 {
			tmpReadyIndex = tmpIndexArray[len(tmpIndexArray)-1]
		}
	}

	pod, err := podLister.Pods(endpoint.TargetRef.Namespace).Get(endpoint.TargetRef.Name)

	instanceSet := make(InstanceSet)
	for _, port := range subset.Ports {
		ipPort := fmt.Sprintf("%s-%d", endpoint.IP, port.Port)
		indexPort := fmt.Sprintf("%s-%d", tmpReadyIndex, port.Port)
		tmpWeight := defaultWeight
		if w, ok := indexPortMap[indexPort]; ok {
			if w >= 0 || w <= 100 {
				tmpWeight = w
			}
		}

		address := &Address{
			IP:       endpoint.IP,
			Port:     int(port.Port),
			PodName:  endpoint.TargetRef.Name,
			Index:    tmpReadyIndex,
			Weight:   tmpWeight,
			Healthy:  true,
			Isolate:  isolate,
			Protocol: port.Name + "/" + string(port.Protocol),
		}

		instanceSet[ipPort] = address

		if err != nil {
			continue
		}

		address.Metadata = pod.Labels
	}
	return instanceSet
}

func GetAddressMapFromEndpoints(service *v1.Service, endpoint *v1.Endpoints,
	podLister corelisters.PodLister, indexPortMap util.IndexPortMap) InstanceSet {
	instanceSet := make(InstanceSet)
	workloadKind := service.GetAnnotations()[util.WorkloadKind]
	defaultWeight := util.GetWeightFromService(service)
	hasIndex := workloadKind == "statefulset" || workloadKind == "statefulsetplus" ||
		workloadKind == "StatefulSetPlus" || workloadKind == "StatefulSet"

	res, err := json.Marshal(endpoint)
	if err == nil {
		log.Info("get addresses map from endpoints", zap.String("endpoint", string(res)))
	}

	for _, subset := range endpoint.Subsets {
		for _, readyAds := range subset.Addresses {
			// TODO 后续可以考虑支持只注册某些端口到对应的服务上，而不是默认全部的端口都进行注册
			addresses := buildAddresses(&readyAds, &subset, hasIndex, podLister, defaultWeight, indexPortMap, false)
			for k, v := range addresses {
				instanceSet[k] = v
			}
		}

		// 如果不需要优雅同步，就不把notReadyAddress加入的同步列表里面。
		for _, notReadyAds := range subset.NotReadyAddresses {
			addresses := buildAddresses(&notReadyAds, &subset, hasIndex, podLister, defaultWeight, indexPortMap, true)
			for k, v := range addresses {
				instanceSet[k] = v
			}
		}
	}

	return instanceSet
}

// GetAddressMapFromPolarisInstance 实体转换，将 polaris 接口返回的对象转为后续注册需要的实体
func GetAddressMapFromPolarisInstance(instances []model.Instance, cluster string) InstanceSet {
	instanceSet := make(InstanceSet)
	// 根据metadata要过滤出来对应的实例列表
	/*
		"clusterName":  p.config.PolarisController.ClusterName,
		"namespace":    service.GetNamespace(),
		"workloadName": service.GetAnnotations()[WorkloadName],
		"workloadKind": service.GetAnnotations()[WorkloadKind],
		"serviceName":  service.GetName,
	*/
	for _, instance := range instances {
		clusterName := instance.GetMetadata()[util.PolarisClusterName]
		source := instance.GetMetadata()[util.PolarisSource]

		// 存量使用 platform 字段，这里兼容存量字段
		oldSource := instance.GetMetadata()[util.PolarisOldSource]

		/**
			只处理本 k8s 集群该 service 对应的 polaris 实例。之所以要加上 source ，是因为
		    防止不用 polaris-controller 的客户端，也使用 clusterName 这个 metadata。
		*/
		flag := (clusterName == cluster && source == polarisapi.Source) ||
			(clusterName == cluster && oldSource == polarisapi.Source)

		log.Infof("old source is %s, source %s, cluster is %s", source, oldSource, cluster)

		// 只有本集群且 controller 注册的实例，才由 controller 管理。
		if flag {
			tmpKey := fmt.Sprintf("%s-%d", instance.GetHost(), instance.GetPort())
			instanceSet[tmpKey] = &Address{
				IP:              instance.GetHost(),
				Port:            int(instance.GetPort()),
				Weight:          instance.GetWeight(),
				Protocol:        instance.GetProtocol(),
				PolarisInstance: instance,
				Healthy:         instance.IsHealthy(),
				Isolate:         instance.IsIsolated(),
			}
		}
	}

	return instanceSet
}
