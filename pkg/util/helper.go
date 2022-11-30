/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/polarismesh/polaris-controller/common/log"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
)

var ignoredNamespaces = []string{
	metav1.NamespaceSystem,
	metav1.NamespacePublic,
	"istio-system",
	"polaris-system",
	"kube-node-lease",
}

var controllerNamespace = "polaris-system"
var controllerServiceName = "polaris-controller-metrics"
var controllerNewNamespace = "Polaris"

const (
	DefaultWeight = 100
)

// WaitForAPIServer waits for the API Server's /healthz endpoint to report "ok" with timeout.
func WaitForAPIServer(client clientset.Interface, timeout time.Duration) error {
	var lastErr error

	err := wait.PollImmediate(time.Second, timeout, func() (bool, error) {
		healthStatus := 0
		result := client.Discovery().RESTClient().Get().AbsPath("/healthz").Do().StatusCode(&healthStatus)
		if result.Error() != nil {
			lastErr = fmt.Errorf("failed to get apiserver /healthz status: %v", result.Error())
			return false, nil
		}
		if healthStatus != http.StatusOK {
			content, _ := result.Raw()
			lastErr = fmt.Errorf("APIServer isn't healthy: %v", string(content))
			log.Warnf("APIServer isn't healthy yet: %v. Waiting a little while.", string(content))
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return fmt.Errorf("%v: %v", err, lastErr)
	}

	return nil
}

// CompareServiceAnnotationsChange 比较service变化
func CompareServiceAnnotationsChange(old, new map[string]string) ServiceChangeType {

	// 以下变更,需要同步对应的Service实例信息
	if old[PolarisHeartBeatTTL] != new[PolarisHeartBeatTTL] {
		return InstanceTTLChanged
	}
	if old[PolarisCustomWeight] != new[PolarisCustomWeight] {
		return InstanceCustomWeightChanged
	}
	if old[PolarisMetadata] != new[PolarisMetadata] {
		return InstanceMetadataChanged
	}

	// 以下变更不会引发Service同步
	if old[PolarisWeight] != new[PolarisWeight] {
		return InstanceWeightChanged
	}
	if old[PolarisEnableRegister] != new[PolarisEnableRegister] {
		return InstanceEnableRegisterChanged
	}
	return ""
}

// CompareServiceChange 判断本次更新是什么类型的
func CompareServiceChange(old, new *v1.Service, syncMode string) ServiceChangeType {

	log.Infof("CompareServiceChange new is %v", new.Annotations)

	if !IsPolarisService(new, syncMode) {
		return ServicePolarisDelete
	}
	return CompareServiceAnnotationsChange(old.GetAnnotations(), new.GetAnnotations())
}

// IfNeedCreateServiceAlias Determine whether to create a service alias
func IfNeedCreateServiceAlias(old, new *v1.Service) bool {
	if old.Annotations[PolarisAliasNamespace] != new.Annotations[PolarisAliasNamespace] ||
		old.Annotations[PolarisAliasService] != new.Annotations[PolarisAliasService] {
		if new.Annotations[PolarisAliasNamespace] == "" || new.Annotations[PolarisAliasService] == "" {
			return false
		}
		return true
	}
	return false
}

// IsPolarisService 用于判断是是否满足创建PolarisService的要求字段，这块逻辑应该在webhook中也增加
func IsPolarisService(svc *v1.Service, syncMode string) bool {

	// 过滤一些不合法的 service
	if IgnoreService(svc) {
		return false
	}

	// 按需同步情况下需要判断 service 是否带有 sync 注解
	if syncMode == SyncModeDemand {
		if !IsServiceSyncEnable(svc) {
			return false
		}
	}

	// service 显示标注sync为false不进行同步
	if IsServiceSyncDisable(svc) {
		return false
	}

	return true
}

// IgnoreService 添加 service 时，忽略一些不需要处理的 service
func IgnoreService(svc *v1.Service) bool {
	// sync polaris-controller
	if svc.Namespace == controllerNamespace && svc.Name == controllerServiceName {
		svc.Namespace = controllerNewNamespace
		svc.Name = "polaris-controller" + "." + svc.ClusterName
		return false
	}

	// 默认忽略某些命名空间
	for _, namespaces := range ignoredNamespaces {
		if svc.GetNamespace() == namespaces {
			return true
		}
	}

	// Port是否合法 不能不设置port
	if len(svc.Spec.Ports) < 1 {
		log.Infof("forbidden sync to polaris: Service %s/%s has no ports", svc.GetNamespace(), svc.GetName())
		return true
	}

	// 没有设置 selector，polaris controller 不处理
	if svc.Spec.Selector == nil {
		log.Infof("forbidden sync to polaris: Service %s/%s has no selectors", svc.GetNamespace(), svc.GetName())
		return true
	}

	return false
}

// IgnoreEndpoint 忽略一些命名空间下的 endpoints
func IgnoreEndpoint(endpoint *v1.Endpoints) bool {
	// sync polaris-controller
	if endpoint.Namespace == controllerNamespace && endpoint.Name == controllerServiceName {
		endpoint.Namespace = controllerNewNamespace
		endpoint.Name = "polaris-controller" + "." + endpoint.ClusterName
		return true
	}

	// 默认忽略某些命名空间
	for _, namespaces := range ignoredNamespaces {
		if endpoint.GetNamespace() == namespaces {
			return false
		}
	}
	return true
}

// IgnoreNamespace 忽略一些命名空间
func IgnoreNamespace(namespace *v1.Namespace) bool {
	// 默认忽略某些命名空间
	for _, ns := range ignoredNamespaces {
		if namespace.GetName() == ns {
			return false
		}
	}
	return true
}

// GetWeightFromService 从 k8s service 中获取 weight，如果 service 中没设置，则取默认值
func GetWeightFromService(svc *v1.Service) int {
	weight, ok := svc.GetAnnotations()[PolarisWeight]
	if ok {
		if w, err := strconv.Atoi(weight); err != nil {
			log.Error("error to convert weight ", zap.Error(err))
			return DefaultWeight
		} else {
			return w
		}
	}
	return DefaultWeight
}

// IsNamespaceSyncEnable 命名空间是否启用了 sync 注解
func IsNamespaceSyncEnable(ns *v1.Namespace) bool {
	sync, ok := ns.Annotations[PolarisSync]
	if ok && sync == IsEnableSync {
		return true
	}
	return false
}

// IsServiceSyncEnable service 是否启用了 sync 注解
func IsServiceSyncEnable(service *v1.Service) bool {
	sync, ok := service.Annotations[PolarisSync]
	if ok && sync == IsEnableSync {
		return true
	}
	return false
}

// IsServiceSyncDisable service 是否关闭了 sync 注解
func IsServiceSyncDisable(service *v1.Service) bool {
	sync, ok := service.Annotations[PolarisSync]
	if ok && sync == IsDisableSync {
		return true
	}
	return false
}

// IsServiceHasSyncAnnotation service 是否设置了 sync 注解
func IsServiceHasSyncAnnotation(service *v1.Service) bool {
	_, ok := service.Annotations[PolarisSync]
	return ok
}
