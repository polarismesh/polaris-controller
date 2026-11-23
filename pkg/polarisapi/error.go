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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// ErrDeleteConflict requires authoritative readback before deletion can finish.
var ErrDeleteConflict = errors.New("instance deletion conflict; confirmation required")

type APIError struct {
	Code uint32
	Info string
}

func (e *APIError) Error() string { return fmt.Sprintf("code=%d %s", e.Code, e.Info) }

// FailureSignature excludes request IDs, addresses and response text so repeated
// failures aggregate by error class while a changed business code is visible.
func FailureSignature(err error) string {
	codes := make(map[string]struct{})
	var collect func(error)
	collect = func(err error) {
		if err == nil {
			return
		}
		switch e := err.(type) {
		case *BatchError:
			for _, item := range e.Errors {
				if item.Cause != nil {
					collect(item.Cause)
				} else if item.Code != nil {
					codes[strconv.FormatUint(uint64(*item.Code), 10)] = struct{}{}
				}
			}
		case *APIError:
			codes[strconv.FormatUint(uint64(e.Code), 10)] = struct{}{}
		case model.SDKError:
			codes[fmt.Sprintf("sdk-%d", e.ErrorCode())] = struct{}{}
			if e.ServerCode() != 0 {
				codes[strconv.FormatUint(uint64(e.ServerCode()), 10)] = struct{}{}
			}
		case interface{ Unwrap() []error }:
			for _, child := range e.Unwrap() {
				collect(child)
			}
		case interface{ Unwrap() error }:
			collect(e.Unwrap())
		default:
			var timeout net.Error
			switch {
			case errors.Is(err, context.Canceled):
				codes["cancelled"] = struct{}{}
			case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
				codes["timeout"] = struct{}{}
			default:
				codes[fmt.Sprintf("%T", err)] = struct{}{}
			}
		}
	}
	collect(err)
	result := make([]string, 0, len(codes))
	for code := range codes {
		result = append(result, code)
	}
	sort.Strings(result)
	return strings.Join(result, ",")
}

type BatchError struct {
	Errors []PError
}

func (e *BatchError) Error() string {
	data, err := json.Marshal(e.Errors)
	if err != nil {
		return fmt.Sprintf("%#v", e.Errors)
	}
	return string(data)
}

func (e *BatchError) Is(target error) bool {
	if target != ErrDeleteConflict || len(e.Errors) == 0 {
		return false
	}
	for _, failure := range e.Errors {
		if failure.Code == nil || *failure.Code != SameInstanceRequest {
			return false
		}
	}
	return true
}

func (e *BatchError) Unwrap() []error {
	causes := make([]error, 0, len(e.Errors))
	for _, item := range e.Errors {
		if item.Cause != nil {
			causes = append(causes, item.Cause)
		}
	}
	return causes
}

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
	Cause   error   `json:"-"`
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
		return &BatchError{Errors: append([]PError(nil), pe.e...)}
	}
	return nil
}
