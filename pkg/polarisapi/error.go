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

package polarisapi

import (
	"encoding/json"
	"fmt"
	"sync"
)

// NewPErrors 实例化新的errors
func NewPErrors() *PErrors {
	return &PErrors{}
}

// PErrors API 批量接口错误数据
type PErrors struct {
	m sync.Mutex
	e []PError
}

// PError API 错误数据
type PError struct {
	ID      string  `json:"id,omitempty"`
	PodName string  `json:"podName,omitempty"`
	Port    int     `json:"port,omitempty"`
	IP      string  `json:"ip,omitempty"`
	Code    *uint32 `json:"code,omitempty"`
	Info    string  `json:"info,omitempty"`
}

// Append 增加errors方法
func (pe *PErrors) Append(pError PError) {
	pe.m.Lock()
	defer pe.m.Unlock()
	pe.e = append(pe.e, pError)
}

// GetError 获取error错误
func (pe *PErrors) GetError() error {
	pe.m.Lock()
	defer pe.m.Unlock()

	if len(pe.e) != 0 {
		data, err := json.Marshal(pe.e)
		if err != nil {
			return fmt.Errorf("%#v", pe.e)
		}
		return fmt.Errorf("%s", string(data))
	}
	return nil
}
