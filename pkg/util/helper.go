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

package util

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/common/log"
)

var ignoredNamespaces = []string{
	metav1.NamespaceSystem,
	metav1.NamespacePublic,
	"istio-system",
	"polaris-system",
	"kube-node-lease",
}

const (
	DefaultWeight = 100
)

// WaitForAPIServer waits for the API Server's /healthz endpoint to report "ok" with timeout.
func WaitForAPIServer(client clientset.Interface, timeout time.Duration) error {
	var lastErr error

	err := wait.PollUntilContextTimeout(context.Background(), time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			healthStatus := 0
			result := client.Discovery().RESTClient().Get().AbsPath("/healthz").Do(context.TODO()).StatusCode(&healthStatus)
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

// GetWeightFromService 从 k8s service 中获取 weight，如果 service 中没设置，则取默认值
func GetWeightFromService(svc *v1.Service) int {
	weight, ok := svc.GetAnnotations()[PolarisWeight]
	if ok {
		w, err := strconv.Atoi(weight)
		if err != nil {
			log.Error("error to convert weight ", zap.Error(err))
			return DefaultWeight
		}
		return w
	}
	return DefaultWeight
}

// EnableSync 是否启用了 sync 注解
func EnableSync(obj metav1.Object) bool {
	sync, ok := obj.GetAnnotations()[PolarisSync]
	if ok && sync == IsEnableSync {
		return true
	}
	return false
}

// IgnoreObject 忽略一些命名空间
func IgnoreObject(obj metav1.Object) bool {
	// 默认忽略某些命名空间
	for _, ns := range ignoredNamespaces {
		if obj.GetNamespace() == ns {
			return true
		}
		if obj.GetName() == ns {
			return true
		}
	}
	return false
}

// IgnoreService 添加 service 时，忽略一些不需要处理的 service
func IgnoreService(svc *v1.Service) bool {
	// 默认忽略某些命名空间
	if IgnoreObject(svc) {
		return true
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
