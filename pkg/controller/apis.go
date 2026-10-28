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
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"strconv"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	v1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

const (
	Source         = "polaris-controller"
	defaultMetaNum = 8
	globalToken    = "polaris@12345678"
)

// updateService 根据 Kubernetes Service Label 的实际变化更新 Polaris Service metadata。
func (p *PolarisController) updateService(old, cur *v1.Service) error {
	resp, err := polarisapi.GetService(cur)
	if err != nil {
		return err
	}

	if len(resp.Services) == 0 {
		return fmt.Errorf("not found service:%s", cur.GetNamespace()+"/"+cur.GetName())
	}
	polarisSvc := resp.Services[0]

	metadata := maps.Clone(polarisSvc.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}

	changed := false
	for key, value := range cur.Labels {
		remoteValue, exists := metadata[key]
		if !exists {
			metadata[key] = value
			changed = true
			continue
		}

		oldValue, hadLabel := old.Labels[key]
		if !hadLabel || oldValue != value {
			if remoteValue != value {
				metadata[key] = value
				changed = true
			}
		}
	}

	for key, oldValue := range old.Labels {
		if _, exists := cur.Labels[key]; exists {
			continue
		}

		remoteValue, exists := metadata[key]
		if !exists {
			continue
		}
		if remoteValue != oldValue {
			log.SyncNamingScope().Warnf(
				"Service %s/%s preserved metadata key %q because its value changed outside the removed Label",
				cur.GetNamespace(), cur.GetName(), key)
			continue
		}
		delete(metadata, key)
		changed = true
	}

	if !changed {
		return nil
	}

	polarisSvc.Metadata = metadata
	_, _, err = polarisapi.UpdateService(cur, []polarisapi.Service{polarisSvc})
	return err
}

// addInstances 批量增加实例接口
func (p *PolarisController) addInstances(service *v1.Service, addresses []address.Address) error {
	if len(addresses) == 0 {
		return nil
	}
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	// 处理健康检查
	var healthCheck polarisapi.HealthCheck
	healthy := util.Bool(true)
	enableHealthCheck := util.Bool(false)

	ttlStr := service.GetAnnotations()[util.PolarisHeartBeatTTL]
	if ttlStr != "" {
		// ttl 默认是5s
		ttl, err := strconv.Atoi(ttlStr)
		if err != nil {
			log.SyncNamingScope().Errorf("PolarisHeartBeatTTL params %s is invalid, must [1, 60], now %s", serviceMsg, ttlStr)
		} else {
			if ttl > 0 && ttl <= 60 {
				healthCheck.Heartbeat.TTL = ttl
				*healthy = false
				*enableHealthCheck = true
			}
		}
	}

	instances := make([]polarisapi.Instance, 0, len(addresses))

	// 装载Instances
	for i := range addresses {
		addr := addresses[i]

		metadata := mergeMetadataWithService(service, addr, p.config.PolarisController.ClusterName)
		version := metadata[util.PolarisCustomVersion]
		// 如果没有读取到用户显示设置的自定义版本号，则从 addr 中读取
		if version == "" {
			version = addr.Version
		}

		*healthy = *healthy && addr.Healthy
		tmpInstance := polarisapi.Instance{
			Service:           service.GetName(),
			Namespace:         service.GetNamespace(),
			ServiceToken:      globalToken,
			HealthCheck:       &healthCheck,
			Host:              addr.IP,
			Protocol:          addr.Protocol,
			Version:           version,
			Port:              util.IntPtr(addr.Port),
			Weight:            util.IntPtr(addr.Weight),
			Healthy:           healthy,
			Isolate:           &addr.Isolate,
			EnableHealthCheck: enableHealthCheck,
			Metadata:          metadata,
		}
		instances = append(instances, tmpInstance)
	}

	return polarisapi.AddInstances(instances, p.config.PolarisController.Size, serviceMsg)
}

// deleteInstances 批量删除实例接口
func (p *PolarisController) deleteInstances(service *v1.Service, addresses []address.Address) error {
	if len(addresses) == 0 {
		return nil
	}
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to delete all instance %s (%v)", serviceMsg, time.Since(startTime))
	}()

	var instances []polarisapi.Instance

	for _, i := range addresses {
		tmpInstance := polarisapi.Instance{
			Service:      service.GetName(),
			Namespace:    service.GetNamespace(),
			ServiceToken: globalToken,
			Host:         i.IP,
			Port:         util.IntPtr(i.Port),
		}
		instances = append(instances, tmpInstance)
	}
	return polarisapi.DeleteInstances(instances, p.config.PolarisController.Size, serviceMsg)
}

// updateInstances 批量更新实例接口
func (p *PolarisController) updateInstances(service *v1.Service, addresses []address.Address) error {
	if len(addresses) == 0 {
		return nil
	}
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to update all %s (%v)", serviceMsg, time.Since(startTime))
	}()

	// 处理健康检查
	var healthCheck polarisapi.HealthCheck
	enableHealthCheck := util.Bool(false)

	ttlStr := service.GetAnnotations()[util.PolarisHeartBeatTTL]
	if ttlStr != "" {
		// ttl 默认是5s
		ttl, err := strconv.Atoi(ttlStr)
		if err != nil {
			log.SyncNamingScope().Errorf("PolarisHeartBeatTTL params %s is invalid, must [1, 60], now %s", serviceMsg, ttlStr)
		} else {
			if ttl > 0 && ttl <= 60 {
				healthCheck.Type = util.IntPtr(0)
				healthCheck.Heartbeat.TTL = ttl
				enableHealthCheck = util.Bool(true)
			}
		}
	}

	instances := make([]polarisapi.Instance, 0, len(addresses))

	for i := range addresses {
		addr := addresses[i]

		metadata := mergeMetadataWithService(service, addr, p.config.PolarisController.ClusterName)

		version := metadata[util.PolarisCustomVersion]
		if version == "" {
			version = addr.Version
		}
		healthy := util.Bool(addr.Healthy)
		tmpInstance := polarisapi.Instance{
			Service:           service.GetName(),
			Namespace:         service.GetNamespace(),
			ServiceToken:      globalToken,
			HealthCheck:       &healthCheck,
			Host:              addr.IP,
			Protocol:          addr.Protocol,
			Version:           version,
			Port:              util.IntPtr(addr.Port),
			Weight:            util.IntPtr(addr.Weight),
			Healthy:           healthy,
			Isolate:           &addr.Isolate,
			EnableHealthCheck: enableHealthCheck,
			Metadata:          metadata,
		}
		instances = append(instances, tmpInstance)
	}
	return polarisapi.UpdateInstances(instances, p.config.PolarisController.Size, serviceMsg)
}

// getAllInstance 通过SDK获取全量Instances
func (p *PolarisController) getAllInstance(service *v1.Service) (instances []model.Instance, err error) {

	startTime := time.Now()
	getInstancesReq := &api.GetAllInstancesRequest{}
	getInstancesReq.FlowID = rand.Uint64()
	getInstancesReq.Namespace = service.GetNamespace()
	getInstancesReq.Service = service.GetName()
	registered, err := p.consumer.GetAllInstances(getInstancesReq)
	if err != nil {
		metrics.InstanceRequestSync.WithLabelValues("Get", "SDK", "Failed", "500").
			Observe(time.Since(startTime).Seconds())
		log.SyncNamingScope().Errorf("Fail [%s/%s] sync GetAllInstances, err is %v",
			service.GetNamespace(), service.GetName(), err)
		return nil, err
	}
	metrics.InstanceRequestSync.WithLabelValues("Get", "SDK", "Success", "200").
		Observe(time.Since(startTime).Seconds())

	return registered.GetInstances(), nil
}

// CompareInstance 使用全量实例判断是否新增，仅更新、删除本集群 controller 托管的实例。
func (p *PolarisController) CompareInstance(service *v1.Service,
	spec, currentController, currentAll address.InstanceSet) (addIns,
	deleteIns, updateIns []address.Address) {
	for index, instance := range spec {
		if cur := currentController[index]; cur != nil {
			if p.compareInstanceUpdate(service, instance, cur) {
				log.SyncNamingScope().Warnf("%s %s need update %v", service.Namespace, service.Name, instance.IP)
				updated := *instance
				updated.PolarisInstance = cur.PolarisInstance
				updateIns = append(updateIns, updated)
			}
		} else if currentAll[index] == nil {
			// SDK/Java Agent 或其他集群已注册相同地址时，不重复 Add。
			addIns = append(addIns, *instance)
		}
	}

	// 删除范围保持原有归属限制，不能遍历全量实例。
	for i, ins := range currentController {
		if spec[i] == nil {
			log.SyncNamingScope().Warnf("%s %s need delete %v-%v", service.Namespace, service.Name, ins.IP, ins.Port)
			deleteIns = append(deleteIns, *ins)
		}
	}
	return
}

// CompareInstanceUpdate 比较已存在的实例是否需要更新
func (p *PolarisController) compareInstanceUpdate(service *v1.Service, spec *address.Address,
	cur *address.Address) bool {
	/*
		    HealthCheck:       &healthCheck,
			Weight:            &weight,
			EnableHealthCheck: enableHealthCheck,
			Metadata:          metadata,
	*/
	// 处理健康检查
	var healthCheck polarisapi.HealthCheck
	enableHealthCheck := util.Bool(false)

	// health check update
	ttlStr := service.GetAnnotations()[util.PolarisHeartBeatTTL]
	if ttlStr != "" {
		// ttl 默认是5s
		ttl, err := strconv.Atoi(ttlStr)
		if err != nil {
			log.SyncNamingScope().Errorf("annotation 'polarismesh.cn/ttl' value: %s, converted to type int error %v",
				ttlStr, err)
		} else {
			if ttl > 0 && ttl <= 60 {
				healthCheck.Type = util.IntPtr(0)
				healthCheck.Heartbeat.TTL = ttl
				*enableHealthCheck = true
			}
		}
	}

	if cur.PolarisInstance.IsEnableHealthCheck() != *enableHealthCheck {
		return true
	}

	// healthy update
	if cur.PolarisInstance.IsHealthy() != spec.Healthy {
		return true
	}

	// isolate update
	if cur.PolarisInstance.IsIsolated() != spec.Isolate {
		return true
	}

	// weight update
	if cur.Weight != spec.Weight {
		return true
	}

	// protocol update
	if cur.Protocol != spec.Protocol {
		return true
	}

	// 使用与写入完全相同的 metadata，覆盖 Pod/Service 字段的增加、变更和删除。
	desired := *spec
	desired.PolarisInstance = cur.PolarisInstance
	metadata := mergeMetadataWithService(service, desired, p.config.PolarisController.ClusterName)
	return !maps.Equal(metadata, cur.PolarisInstance.GetMetadata())
}

// filterPolarisMetadata 过滤属于TKE注册的服务
func (p *PolarisController) filterPolarisMetadata(service *v1.Service, instances []model.Instance) []address.Address {
	var ins []address.Address

	// 根据metadata要过滤出来对应的实例列表
	/*
		"clusterName":  p.config.PolarisController.ClusterName,
		"namespace":    service.GetNamespace(),
		"workloadName": service.GetAnnotations()[WorkloadName],
		"workloadKind": service.GetAnnotations()[WorkloadKind],
		"serviceName":  service.GetName,
	*/
	for _, instance := range instances {
		// 新增字段flag，或者使用原来判断条件
		if p.isSyncInstance(service, instance) {
			ins = append(ins, address.Address{
				IP:   instance.GetHost(),
				Port: int(instance.GetPort()),
			})
		}
	}
	return ins
}

func (p *PolarisController) isSyncInstance(service *v1.Service, ins model.Instance) bool {
	clusterName := ins.GetMetadata()[util.PolarisClusterName]
	source := ins.GetMetadata()[util.PolarisSource]

	// 存量使用 platform 字段，这里兼容存量字段
	oldSource := ins.GetMetadata()[util.PolarisOldSource]

	match := (clusterName == p.config.PolarisController.ClusterName && source == Source) ||
		(clusterName == p.config.PolarisController.ClusterName && oldSource == Source)
	if !match {
		return false
	}

	if instanceNamespace := ins.GetMetadata()[util.K8sServiceNamespace]; instanceNamespace != "" &&
		instanceNamespace != service.GetNamespace() {
		return false
	}
	if instanceName := ins.GetMetadata()[util.K8sServiceName]; instanceName != "" &&
		instanceName != service.GetName() {
		return false
	}
	instanceUID := ins.GetMetadata()[util.K8sServiceUID]
	return instanceUID == "" || service.GetUID() == "" || instanceUID == string(service.GetUID())
}

// getCustomWeight 将用户配置的权重
func getCustomWeight(service *v1.Service, serviceMsg string) (indexPortMap util.IndexPortMap) {
	customWeightStr := service.GetAnnotations()[util.PolarisCustomWeight]
	if customWeightStr == "" {
		return
	}
	err := json.Unmarshal([]byte(customWeightStr), &indexPortMap)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed %s unmarshal user %s,err %v", serviceMsg, util.PolarisCustomWeight, err)
	}
	return
}

func mergeMetadataWithService(service *v1.Service, addr address.Address, clusterName string) map[string]string {
	metadataStr := service.GetAnnotations()[util.PolarisMetadata]
	metadata := make(map[string]string)

	if metadataStr != "" {
		_ = json.Unmarshal([]byte(metadataStr), &metadata)
	}
	if metadata == nil {
		metadata = make(map[string]string)
	}

	for k, v := range addr.Metadata {
		metadata[k] = v
	}

	// 保留历史默认字段；其余业务 metadata 由 Service 注解和 Pod 标签完整声明。
	if addr.PolarisInstance != nil {
		for k, v := range addr.PolarisInstance.GetMetadata() {
			_, isDefault := util.PolarisDefaultMetaSet[k]
			if _, exists := metadata[k]; !exists && (isDefault || k == util.PolarisOldSource) {
				metadata[k] = v
			}
		}
	}
	// 实例归属不能被 Service 自定义 metadata 或 Pod 标签覆盖。
	metadata[util.PolarisSource] = Source
	metadata[util.PolarisClusterName] = clusterName
	metadata[util.K8sServiceNamespace] = service.GetNamespace()
	metadata[util.K8sServiceName] = service.GetName()
	metadata[util.K8sServiceUID] = string(service.GetUID())

	return metadata
}
