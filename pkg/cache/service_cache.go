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

package cache

import (
	"sync"

	v1 "k8s.io/api/core/v1"
)

// CachedServiceMap
func NewCachedServiceMap() *CachedServiceMap {
	return &CachedServiceMap{}
}

// CachedServiceMap key：string， value: *v1.service
type CachedServiceMap struct {
	sm sync.Map
}

// Delete
func (csm *CachedServiceMap) Delete(key string) {
	csm.sm.Delete(key)
}

// Load
func (csm *CachedServiceMap) Load(key string) (value *v1.Service, ok bool) {
	v, ok := csm.sm.Load(key)
	if v != nil {
		value, ok2 := v.(*v1.Service)
		if !ok2 {
			ok = false
		}
		return value, ok
	}
	return value, ok
}

// Store
func (csm *CachedServiceMap) Store(key string, value *v1.Service) {
	csm.sm.Store(key, value)
}
