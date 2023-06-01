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

package cache

import (
	"sync"

	v1 "k8s.io/api/core/v1"
)

// CachedConfigFileMap
func NewCachedConfigFileMap() *CachedConfigFileMap {
	return &CachedConfigFileMap{}
}

// CachedConfigFileMap key：string， value: *v1.ConfigFile
type CachedConfigFileMap struct {
	sm sync.Map
}

// Delete
func (csm *CachedConfigFileMap) Delete(key string) {
	csm.sm.Delete(key)
}

// Load
func (csm *CachedConfigFileMap) Load(key string) (value *v1.ConfigMap, ok bool) {
	v, ok := csm.sm.Load(key)
	if v != nil {
		value, ok2 := v.(*v1.ConfigMap)
		if !ok2 {
			ok = false
		}
		return value, ok
	}
	return value, ok
}

// Store
func (csm *CachedConfigFileMap) Store(key string, value *v1.ConfigMap) {
	csm.sm.Store(key, value)
}

// Clear remove all elements in cache
func (csm *CachedConfigFileMap) Clear() {
	csm.sm.Range(func(key interface{}, value interface{}) bool {
		csm.sm.Delete(key)
		return true
	})
}

// Range execute f for each element of cache
func (csm *CachedConfigFileMap) Range(f func(key string, value *v1.ConfigMap) bool) {
	csm.sm.Range(func(key, value interface{}) bool {
		return f(key.(string), value.(*v1.ConfigMap))
	})
}
