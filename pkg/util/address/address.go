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

	"github.com/polarismesh/polaris-go/pkg/model"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
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
	Version         string
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
	podLister corelisters.PodLister, defaultWeight int,
	indexPortMap util.IndexPortMap, customProtocols map[string]string, isolate bool) (InstanceSet, error) {
	if endpoint.TargetRef == nil {
		return nil, fmt.Errorf("endpoint %s has no Pod target reference", endpoint.IP)
	}
	pod, err := podLister.Pods(endpoint.TargetRef.Namespace).Get(endpoint.TargetRef.Name)
	if err != nil {
		return nil, fmt.Errorf("get Pod %s/%s for endpoint %s: %w",
			endpoint.TargetRef.Namespace, endpoint.TargetRef.Name, endpoint.IP, err)
	}

	tmpReadyIndex := ""
	if hasIndex {
		tmpIndexArray := strings.Split(endpoint.TargetRef.Name, "-")
		if len(tmpIndexArray) > 1 {
			tmpReadyIndex = tmpIndexArray[len(tmpIndexArray)-1]
		}
	}

	instanceSet := make(InstanceSet)
	for _, port := range subset.Ports {
		ipPort := fmt.Sprintf("%s-%d", endpoint.IP, port.Port)
		indexPort := fmt.Sprintf("%s-%d", tmpReadyIndex, port.Port)
		protocol := string(port.Protocol)
		if port.Name != "" {
			if custom, ok := customProtocols[port.Name]; ok {
				protocol = custom
			}
		}
		if protocol == "" {
			protocol = string(v1.ProtocolTCP)
		}
		tmpWeight := defaultWeight
		if w, ok := indexPortMap[indexPort]; ok {
			if w >= 0 && w <= 100 {
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
			Protocol: protocol,
		}

		instanceSet[ipPort] = address
		address.Metadata = noEmptyMap(pod.Labels)
		// 默认从 POD 的 labels 中获取到 version 字段的值
		address.Version = address.Metadata["version"]
	}
	return instanceSet, nil
}

func parseCustomProtocols(service *v1.Service) (map[string]string, error) {
	raw := service.GetAnnotations()[util.PolarisProtocols]
	if raw == "" {
		return nil, nil
	}

	protocols := make(map[string]string)
	if err := json.Unmarshal([]byte(raw), &protocols); err != nil {
		return nil, fmt.Errorf("annotation %s must be a JSON object of string values: %w",
			util.PolarisProtocols, err)
	}
	if protocols == nil {
		return nil, fmt.Errorf("annotation %s must be a JSON object of string values", util.PolarisProtocols)
	}

	namedPorts := make(map[string]struct{}, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		if port.Name != "" {
			namedPorts[port.Name] = struct{}{}
		}
	}

	for name, protocol := range protocols {
		if name == "" {
			return nil, fmt.Errorf("annotation %s cannot use an empty port name", util.PolarisProtocols)
		}
		if _, exists := namedPorts[name]; !exists {
			return nil, fmt.Errorf("annotation %s references unknown Service port name %q", util.PolarisProtocols, name)
		}
		if protocol == "" || strings.TrimSpace(protocol) != protocol {
			return nil, fmt.Errorf("annotation %s has an invalid protocol for port %q: %q",
				util.PolarisProtocols, name, protocol)
		}
		if len(protocol) > util.MaxInstanceProtocolLength {
			return nil, fmt.Errorf("annotation %s protocol for port %q exceeds %d characters",
				util.PolarisProtocols, name, util.MaxInstanceProtocolLength)
		}
	}

	return protocols, nil
}

var _emptyPodLabels = map[string]string{}

func noEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return _emptyPodLabels
	}
	return m
}

// GetAddressMapFromEndpoints 返回完整的地址和 Pod 标签快照。
// Pod 查询失败时返回错误，不向调用方提供可被误用于实例对账的部分地址集合。
func GetAddressMapFromEndpoints(service *v1.Service, endpoint *v1.Endpoints,
	podLister corelisters.PodLister, indexPortMap util.IndexPortMap) (InstanceSet, error) {
	instanceSet := make(InstanceSet)
	workloadKind := service.GetAnnotations()[util.WorkloadKind]
	defaultWeight := util.GetWeightFromService(service)
	customProtocols, err := parseCustomProtocols(service)
	if err != nil {
		return nil, err
	}
	hasIndex := workloadKind == "statefulset" || workloadKind == "statefulsetplus" ||
		workloadKind == "StatefulSetPlus" || workloadKind == "StatefulSet"

	log.SyncNamingScope().Debug("get addresses map from endpoints", zap.Any("endpoint", endpoint))

	for _, subset := range endpoint.Subsets {
		for _, readyAds := range subset.Addresses {
			// TODO 后续可以考虑支持只注册某些端口到对应的服务上，而不是默认全部的端口都进行注册
			addresses, err := buildAddresses(&readyAds, &subset, hasIndex, podLister, defaultWeight,
				indexPortMap, customProtocols, false)
			if err != nil {
				return nil, err
			}
			for k, v := range addresses {
				instanceSet[k] = v
			}
		}

		// 如果不需要优雅同步，就不把notReadyAddress加入的同步列表里面。
		for _, notReadyAds := range subset.NotReadyAddresses {
			addresses, err := buildAddresses(&notReadyAds, &subset, hasIndex, podLister, defaultWeight,
				indexPortMap, customProtocols, true)
			if err != nil {
				return nil, err
			}
			for k, v := range addresses {
				instanceSet[k] = v
			}
		}
	}

	return instanceSet, nil
}

// GetAddressMapFromPolarisInstance 同时返回本集群 controller 托管实例和全量实例的地址索引。
// 全量索引只用于判断地址是否已注册，不扩大 controller 的更新和删除范围。
func GetAddressMapFromPolarisInstance(
	instances []model.Instance, cluster string,
) (currentController, currentAll InstanceSet) {
	currentController = make(InstanceSet)
	currentAll = make(InstanceSet)
	// 根据metadata要过滤出来对应的实例列表
	/*
		"clusterName":  p.config.PolarisController.ClusterName,
		"namespace":    service.GetNamespace(),
		"workloadName": service.GetAnnotations()[WorkloadName],
		"workloadKind": service.GetAnnotations()[WorkloadKind],
		"serviceName":  service.GetName,
	*/
	for _, instance := range instances {
		tmpKey := fmt.Sprintf("%s-%d", instance.GetHost(), instance.GetPort())
		addr := &Address{
			IP:              instance.GetHost(),
			Port:            int(instance.GetPort()),
			Weight:          instance.GetWeight(),
			Protocol:        instance.GetProtocol(),
			PolarisInstance: instance,
			Healthy:         instance.IsHealthy(),
			Isolate:         instance.IsIsolated(),
		}
		currentAll[tmpKey] = addr

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

		log.SyncNamingScope().Debugf(
			"instance source=%s legacySource=%s cluster=%s", source, oldSource, cluster)

		// 只有本集群且 controller 注册的实例，才由 controller 管理。
		if flag {
			currentController[tmpKey] = addr
		}
	}

	return currentController, currentAll
}
