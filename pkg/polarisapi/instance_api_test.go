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
	"net/http"
	"strconv"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func queryTestService() *v1.Service {
	return &v1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "svc"}}
}

func queryTestInstance(id int) Instance {
	return Instance{ID: fmt.Sprintf("id-%d", id), Namespace: "ns", Service: "svc",
		Host: fmt.Sprintf("10.0.0.%d", id), Port: util.IntPtr(80)}
}

func queryPageResponse(t *testing.T, amount uint32, instances []Instance) *http.Response {
	t.Helper()
	size := uint32(len(instances))
	body, err := json.Marshal(GetInstancesResponse{
		Code: ExecuteSuccess, Amount: &amount, Size: &size, Instances: instances,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, _ := aliasAPIResponse(http.StatusOK, string(body))
	return response
}

func TestGetInstancesUsesBoundedConsolePagination(t *testing.T) {
	service := queryTestService()
	service.Namespace, service.Name = "ns & encoded", "svc+encoded"
	calls := 0
	newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || request.URL.Path != "/naming/v1/instances" {
			t.Fatalf("unexpected instance query: %s %s", request.Method, request.URL)
		}
		deadline, hasDeadline := request.Context().Deadline()
		if !hasDeadline || time.Until(deadline) > instanceQueryTimeout || time.Until(deadline) <= 0 {
			t.Fatal("instance query has no bounded deadline")
		}
		if request.Header.Get(AccessTokenHeader) != "test-token" || request.Header.Get("Request-Id") == "" {
			t.Fatal("instance query omitted authentication or request ID")
		}
		query := request.URL.Query()
		if query.Get("namespace") != service.Namespace || query.Get("service") != service.Name ||
			query.Get("limit") != strconv.Itoa(instanceQueryPageSize) {
			t.Fatalf("query escaping or page size is incorrect: %+v", query)
		}
		offset, err := strconv.Atoi(query.Get("offset"))
		if err != nil || offset != (calls-1)*instanceQueryPageSize {
			t.Fatalf("invalid page offset: %s", query.Get("offset"))
		}
		end := offset + instanceQueryPageSize
		if end > 201 {
			end = 201
		}
		var instances []Instance
		for i := offset; i < end; i++ {
			instance := queryTestInstance(i)
			instance.Namespace, instance.Service = service.Namespace, service.Name
			instances = append(instances, instance)
		}
		return queryPageResponse(t, 201, instances), nil
	})
	instances, err := GetInstances(context.Background(), service)
	if err != nil || len(instances) != 201 || calls != 3 {
		t.Fatalf("instances=%d calls=%d error=%v", len(instances), calls, err)
	}
}

func TestGetInstancesRejectsIncompleteOrInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "HTTP error", status: http.StatusForbidden, body: `{"code":200000,"amount":0,"size":0}`},
		{name: "business error", status: http.StatusOK, body: `{"code":401000,"amount":0,"size":0}`},
		{name: "invalid JSON", status: http.StatusOK, body: `{`},
		{name: "missing amount", status: http.StatusOK, body: `{"code":200000,"size":0,"instances":[]}`},
		{name: "missing size", status: http.StatusOK, body: `{"code":200000,"amount":0,"instances":[]}`},
		{name: "missing page", status: http.StatusOK, body: `{"code":200000,"amount":1,"size":0,"instances":[]}`},
		{name: "size mismatch", status: http.StatusOK, body: `{"code":200000,"amount":1,"size":1,"instances":[]}`},
		{name: "count mismatch", status: http.StatusOK,
			body: `{"code":200000,"amount":0,"size":1,"instances":[{"namespace":"ns","service":"svc","host":"10.0.0.1","port":80}]}`},
		{name: "missing port", status: http.StatusOK,
			body: `{"code":200000,"amount":1,"size":1,"instances":[{"namespace":"ns","service":"svc","host":"10.0.0.1"}]}`},
		{name: "invalid port", status: http.StatusOK,
			body: `{"code":200000,"amount":1,"size":1,"instances":[{"namespace":"ns","service":"svc","host":"10.0.0.1","port":70000}]}`},
		{name: "missing host", status: http.StatusOK,
			body: `{"code":200000,"amount":1,"size":1,"instances":[{"namespace":"ns","service":"svc","port":80}]}`},
		{name: "other namespace", status: http.StatusOK,
			body: `{"code":200000,"amount":1,"size":1,"instances":[{"namespace":"other","service":"svc","host":"10.0.0.1","port":80}]}`},
		{name: "other service", status: http.StatusOK,
			body: `{"code":200000,"amount":1,"size":1,"instances":[{"namespace":"ns","service":"other","host":"10.0.0.1","port":80}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
				return aliasAPIResponse(test.status, test.body)
			})
			instances, err := GetInstances(context.Background(), queryTestService())
			if err == nil || instances != nil {
				t.Fatalf("invalid instance snapshot accepted: instances=%+v error=%v", instances, err)
			}
		})
	}
}

func TestGetInstancesRejectsChangingOrRepeatedPages(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "repeated page", true: "count changes"}[changed], func(t *testing.T) {
			calls := 0
			newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				amount := uint32(2)
				if changed && calls > 1 {
					amount++
				}
				return queryPageResponse(t, amount, []Instance{queryTestInstance(1)}), nil
			})
			instances, err := GetInstances(context.Background(), queryTestService())
			if err == nil || instances != nil || calls != 2 {
				t.Fatalf("unstable pagination accepted: instances=%+v calls=%d error=%v", instances, calls, err)
			}
		})
	}
}

func TestGetInstancesAcceptsConfirmedEmptySnapshot(t *testing.T) {
	newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
		return queryPageResponse(t, 0, nil), nil
	})
	instances, err := GetInstances(context.Background(), queryTestService())
	if err != nil || len(instances) != 0 {
		t.Fatalf("empty snapshot rejected: %+v %v", instances, err)
	}
}

func TestGetInstancesDeadlineIncludesOperatorTokenLookup(t *testing.T) {
	for _, operator := range []string{"", "operator-id"} {
		t.Run("operator="+operator, func(t *testing.T) {
			called := false
			newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
				called = true
				wantPath := "/naming/v1/instances"
				if operator != "" {
					wantPath = "/core/v1/user/token"
				}
				if request.URL.Path != wantPath {
					t.Fatalf("unexpected request before timeout: %s", request.URL.Path)
				}
				if _, ok := request.Context().Deadline(); !ok {
					t.Fatal("request omitted context deadline")
				}
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
			PolarisOperator = operator
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			instances, err := GetInstances(ctx, queryTestService())
			if !called || !errors.Is(err, context.DeadlineExceeded) || instances != nil {
				t.Fatalf("deadline was not propagated: called=%v instances=%+v error=%v", called, instances, err)
			}
		})
	}
}

func TestGetInstancesValidatesSourceBeforeNetwork(t *testing.T) {
	newPolarisTestClient(t, func(request *http.Request) (*http.Response, error) {
		t.Fatal("invalid Service reached the network")
		return nil, errors.New("unexpected network request")
	})
	for _, service := range []*v1.Service{nil, {}, {ObjectMeta: metav1.ObjectMeta{Name: "svc"}}} {
		if _, err := GetInstances(context.Background(), service); err == nil {
			t.Fatal("invalid query source accepted")
		}
	}
}
