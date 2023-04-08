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
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

var (
	keyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc
)

func GenObjectQueueKey(obj interface{}) (string, error) {
	key, err := keyFunc(obj)
	if err != nil {
		return "", err
	}

	return key, err
}

// GetOriginKeyWithResyncQueueKey 通过同步任务key生成原始key
func GetOriginKeyWithResyncQueueKey(key string) string {
	return key[:len(key)-len("~resync")]
}

// GenServiceResyncQueueKeyWithOrigin 通过原始key生成用于同步任务的key便于区分不同的任务
func GenServiceResyncQueueKeyWithOrigin(key string) string {
	return key + "~" + "resync"
}

// GenServiceQueueKeyWithFlag 在 namespace 的事件流程中使用。
// 产生 service queue 中的 key，flag 表示添加时是否是北极星的服务
func GenServiceQueueKeyWithFlag(svc *v1.Service, flag string) (string, error) {
	key, err := keyFunc(svc)
	if err != nil {
		return "", err
	}
	key += "~" + flag

	return key, nil
}

// GenServiceQueueKey 产生 service 中 queue 中用的 key
func GenServiceQueueKey(svc *v1.Service) (string, error) {
	key, err := keyFunc(svc)
	if err != nil {
		return "", err
	}

	return key, nil
}

// GetServiceRealKeyWithFlag 从 service queue 中的 key ，解析出 namespace、service、flag
func GetServiceRealKeyWithFlag(queueKey string) (string, string, string, string, error) {
	if queueKey == "" {
		return "", "", "", "", nil
	}
	op := ""
	ss := strings.Split(queueKey, "~")
	namespace, service, err := cache.SplitMetaNamespaceKey(ss[0])
	if err != nil {
		return "", "", "", "", err
	}
	if len(ss) != 1 {
		op = ss[1]
	}
	return ss[0], namespace, service, op, nil
}
