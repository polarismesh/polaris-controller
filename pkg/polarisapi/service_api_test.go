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
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package polarisapi

import (
	"strings"
	"testing"
)

func TestDealAddInstanceResponseSimpleSuccessAndIdempotentCodes(t *testing.T) {
	for _, code := range []uint32{200000, ExistedResource, SameInstanceRequest} {
		t.Run(simpleCodeName(code), func(t *testing.T) {
			errors := NewPErrors()
			dealAddInstanceResponse(AddResponse{Code: code}, "svc", 0, 1, "request", errors)
			if err := errors.GetError(); err != nil {
				t.Fatalf("code %d returned error: %v", code, err)
			}
		})
	}
}

func TestDealAddInstanceResponsePerInstance400208(t *testing.T) {
	errors := NewPErrors()
	response := AddResponse{
		Code: 200000,
		Responses: []InstanceResponse{{
			Code:     SameInstanceRequest,
			Instance: SimpleInstance{Host: "10.0.0.1", Port: 80},
		}},
	}

	dealAddInstanceResponse(response, "svc", 0, 1, "request", errors)
	if err := errors.GetError(); err != nil {
		t.Fatalf("same instance response returned error: %v", err)
	}
	if got := addInstanceRequestStatus(response); got != "Idempotent" {
		t.Fatalf("status=%q want Idempotent", got)
	}
}

func TestDealAddInstanceResponse400208DoesNotHideOtherFailure(t *testing.T) {
	errors := NewPErrors()
	response := AddResponse{
		Code: 200000,
		Responses: []InstanceResponse{
			{
				Code:     SameInstanceRequest,
				Instance: SimpleInstance{Host: "10.0.0.1", Port: 80},
			},
			{
				Code:     500001,
				Info:     "real failure",
				Instance: SimpleInstance{Host: "10.0.0.2", Port: 80},
			},
		},
	}

	dealAddInstanceResponse(response, "svc", 0, 1, "request", errors)
	err := errors.GetError()
	if err == nil || !strings.Contains(err.Error(), `"code":500001`) {
		t.Fatalf("real failure was not preserved: %v", err)
	}
	if strings.Contains(err.Error(), `"code":400208`) {
		t.Fatalf("idempotent response leaked into errors: %v", err)
	}
	if got := addInstanceRequestStatus(response); got != "Failed" {
		t.Fatalf("status=%q want Failed", got)
	}
}

func TestDealAddInstanceResponseTopLevel400208WithResponses(t *testing.T) {
	errors := NewPErrors()
	response := AddResponse{
		Code: SameInstanceRequest,
		Responses: []InstanceResponse{{
			Code:     500001,
			Info:     "real failure",
			Instance: SimpleInstance{Host: "10.0.0.2", Port: 80},
		}},
	}

	dealAddInstanceResponse(response, "svc", 0, 1, "request", errors)
	err := errors.GetError()
	if err == nil || !strings.Contains(err.Error(), `"code":500001`) {
		t.Fatalf("top-level idempotent response hid real failure: %v", err)
	}
	if got := addInstanceRequestStatus(response); got != "Failed" {
		t.Fatalf("status=%q want Failed", got)
	}
}

func simpleCodeName(code uint32) string {
	switch code {
	case 200000:
		return "success"
	case ExistedResource:
		return "existed"
	case SameInstanceRequest:
		return "same-instance"
	default:
		return "unknown"
	}
}
