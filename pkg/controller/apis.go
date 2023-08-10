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

// updateService 批量增加实例接口
func (p *PolarisController) updateService(cur *v1.Service) error {
	resp, err := polarisapi.GetService(cur)
	if err != nil {
		return err
	}

	if len(resp.Services) == 0 {
		return fmt.Errorf("not found service:%s", cur.GetNamespace()+"/"+cur.GetName())
	}
	polarisSvc := resp.Services[0]

	// 合并服务的 labels 信息
	newLabels := cur.Labels
	curLabels := polarisSvc.Metadata
	for k, v := range newLabels {
		curLabels[k] = v
	}

	polarisSvc.Metadata = curLabels
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

		*healthy = *healthy && addr.Healthy
		tmpInstance := polarisapi.Instance{
			Service:           service.Name,
			Namespace:         service.Namespace,
			ServiceToken:      globalToken,
			HealthCheck:       &healthCheck,
			Host:              addr.IP,
			Protocol:          addr.Protocol,
			Version:           metadata[util.PolarisCustomVersion],
			Port:              util.IntPtr(addr.Port),
			Weight:            util.IntPtr(addr.Weight),
			Healthy:           healthy,
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
			Service:      service.Name,
			Namespace:    service.Namespace,
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

		healthy := util.Bool(addr.Healthy)
		tmpInstance := polarisapi.Instance{
			Service:           service.Name,
			Namespace:         service.Namespace,
			ServiceToken:      globalToken,
			HealthCheck:       &healthCheck,
			Host:              addr.IP,
			Port:              util.IntPtr(addr.Port),
			Weight:            util.IntPtr(addr.Weight),
			Healthy:           healthy,
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
	getInstancesReq.Namespace = service.Namespace
	getInstancesReq.Service = service.Name

	registered, err := p.consumer.GetAllInstances(getInstancesReq)
	if err != nil {
		metrics.InstanceRequestSync.WithLabelValues("Get", "SDK", "Failed", "500").
			Observe(time.Since(startTime).Seconds())
			log.SyncNamingScope().Errorf("Fail [%s/%s] sync GetAllInstances, err is %v, return empty",
			service.GetNamespace(), service.GetName(), err)
		return []model.Instance{}, nil
	}
	metrics.InstanceRequestSync.WithLabelValues("Get", "SDK", "Success", "200").
		Observe(time.Since(startTime).Seconds())

	return registered.GetInstances(), nil
}

// CompareInstance 比较预期示例和现有实例的区别
func (p *PolarisController) CompareInstance(service *v1.Service,
	spec, cur address.InstanceSet) (addIns,
	deleteIns, updateIns []address.Address) {
	// 对比预期的endpoint和当前的北极星获取的值，如果没有就增加，如果更新
	for index, instance := range spec {
		if cur[index] != nil {
			// 如果存在，判断是否要更新
			if p.compareInstanceUpdate(service, instance, cur[index]) {
				log.SyncNamingScope().Warnf("%s %s need update %v", service.Namespace, service.Name, instance.IP)
				updateIns = append(updateIns, *instance)
			}
		} else {
			// 如果不存在，增加到add列表
			addIns = append(addIns, *instance)
		}
	}

	// 对比当前北极星的跟预期列表，删除没有用的。
	for i, ins := range cur {
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
			ttl = 5
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

	// weight update
	if cur.Weight != spec.Weight {
		return true
	}

	// protocol update
	if cur.Protocol != spec.Protocol {
		return true
	}

	// custom meta update
	newMetadataStr := service.GetAnnotations()[util.PolarisMetadata]
	oldMetadata := cur.PolarisInstance.GetMetadata()
	if oldMetadata == nil {
		return true
	}

	if newMetadataStr == "" {
		if isPolarisInstanceHasCustomMeta(oldMetadata) {
			return true
		}
		return false
	}
	newMetaMap := make(map[string]string)
	err := json.Unmarshal([]byte(newMetadataStr), &newMetaMap)
	if err != nil {
		log.SyncNamingScope().Errorf("fail to unmarshal json from service annotations %s, error %v", newMetadataStr, err)
		return false
	}

	for k, v := range newMetaMap {
		// 这里的 meta ，后面的流程会覆盖掉，不用处理
		if _, ok := util.PolarisSystemMetaSet[k]; ok {
			continue
		}
		// 不是系统 meta ，查看 polaris ins 中是否有这个 meta key
		curV, ok := oldMetadata[k]
		if !ok {
			// polaris ins 中没有，则是修改
			return true
		}
		// polaris ins 中有，查看 value 是否相同
		if curV != v {
			// 不同，则是修改
			return true
		}
	}
	return false
}

// 判断当前 polaris 实例是否有自定义 meta
func isPolarisInstanceHasCustomMeta(m map[string]string) bool {
	return len(m) > len(util.PolarisDefaultMetaSet)
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
		if p.isSyncInstance(instance) {
			ins = append(ins, address.Address{
				IP:   instance.GetHost(),
				Port: int(instance.GetPort()),
			})
		}
	}
	return ins
}

func (p *PolarisController) isSyncInstance(ins model.Instance) bool {
	clusterName := ins.GetMetadata()[util.PolarisClusterName]
	source := ins.GetMetadata()[util.PolarisSource]

	// 存量使用 platform 字段，这里兼容存量字段
	oldSource := ins.GetMetadata()[util.PolarisOldSource]

	match := (clusterName == p.config.PolarisController.ClusterName && source == Source) ||
		(clusterName == p.config.PolarisController.ClusterName && oldSource == Source)
	return match
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

	metadata[util.PolarisSource] = Source
	metadata[util.PolarisClusterName] = clusterName

	for k, v := range addr.Metadata {
		metadata[k] = v
	}

	return metadata
}
