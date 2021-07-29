package address

import (
	"encoding/json"
	"fmt"
	"git.code.oa.com/polaris/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"strings"
)

// Address 记录IP端口信息
type Address struct {
	IP              string
	Port            int
	PodName         string
	Index           string // stsp,sts才存在有序Pod
	Weight          int
	Healthy         bool
	PolarisInstance model.Instance
}

// InstanceSet 实例信息表，以ip-port作为key
type InstanceSet map[string]*Address

// GetAddressMapFromEndpoints
//  形成
//  [{
//  "10.10.10.10-80": {
//  "ip": "10.10.10.10",
//  "port": "80"
//   }
//  }]
func GetAddressMapFromEndpoints(service *v1.Service, endpoint *v1.Endpoints,
	indexPortMap util.IndexPortMap) InstanceSet {
	instanceSet := make(InstanceSet)
	workloadKind := service.GetAnnotations()[util.WorkloadKind]
	defaultWeight := util.GetWeightFromService(service)
	hasIndex := workloadKind == "statefulset" || workloadKind == "statefulsetplus" ||
		workloadKind == "StatefulSetPlus" || workloadKind == "StatefulSet"

	res, _ := json.Marshal(endpoint)
	klog.Infof("get endpoints %s", string(res))

	for _, subset := range endpoint.Subsets {
		for _, readyAds := range subset.Addresses {
			tmpReadyIndex := ""
			if hasIndex {
				tmpIndexArray := strings.Split(readyAds.TargetRef.Name, "-")
				if len(tmpIndexArray) > 1 {
					tmpReadyIndex = tmpIndexArray[len(tmpIndexArray)-1]
				}
			}
			for _, port := range subset.Ports {
				ipPort := fmt.Sprintf("%s-%d", readyAds.IP, port.Port)
				indexPort := fmt.Sprintf("%s-%d", tmpReadyIndex, port.Port)
				tmpWeight := defaultWeight
				if w, ok := indexPortMap[indexPort]; ok {
					if w >= 0 || w <= 100 {
						tmpWeight = w
					}
				}
				instanceSet[ipPort] = &Address{
					IP:      readyAds.IP,
					Port:    int(port.Port),
					PodName: readyAds.TargetRef.Name,
					Index:   tmpReadyIndex,
					Weight:  tmpWeight,
					Healthy: true,
				}
			}
		}

		// 如果不需要优雅同步，就不把notReadyAddress加入的同步列表里面。
		for _, notReadyAds := range subset.NotReadyAddresses {
			tmpNotReadyIndex := ""
			if hasIndex {
				tmpIndexArray := strings.Split(notReadyAds.TargetRef.Name, "-")
				if len(tmpIndexArray) > 1 {
					tmpNotReadyIndex = tmpIndexArray[len(tmpIndexArray)-1]
				}
			}
			for _, port := range subset.Ports {
				ipPort := fmt.Sprintf("%s-%d", notReadyAds.IP, port.Port)
				indexPort := fmt.Sprintf("%s-%d", tmpNotReadyIndex, port.Port)
				tmpWeight := defaultWeight
				if w, ok := indexPortMap[indexPort]; ok {
					if w >= 0 || w <= 100 {
						tmpWeight = w
					}
				}
				instanceSet[ipPort] = &Address{
					IP:      notReadyAds.IP,
					Port:    int(port.Port),
					PodName: notReadyAds.TargetRef.Name,
					Index:   tmpNotReadyIndex,
					Weight:  tmpWeight,
					Healthy: false,
				}
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
		platform := instance.GetMetadata()[util.PolarisPlatform]

		/**
			只处理本 k8s 集群该 service 对应的 polaris 实例。之所以要加上 platform ，是因为
		    防止不用 polaris-controller 的客户端，也使用 clusterName 这个 metadata。
		*/
		flag := clusterName == cluster && platform == polarisapi.Platform

		// 只有本集群且 controller 注册的实例，才由 controller 管理。
		if flag {
			tmpKey := fmt.Sprintf("%s-%d", instance.GetHost(), instance.GetPort())
			instanceSet[tmpKey] = &Address{
				IP:              instance.GetHost(),
				Port:            int(instance.GetPort()),
				Weight:          instance.GetWeight(),
				PolarisInstance: instance,
				Healthy:         instance.IsHealthy(),
			}
		}
	}

	return instanceSet
}
