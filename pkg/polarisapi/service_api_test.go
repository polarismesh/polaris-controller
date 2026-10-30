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
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestGetPolarisNameIgnoresLegacyOverrideAnnotations(t *testing.T) {
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "demo",
			Annotations: map[string]string{
				"polarismesh.cn/overideNamespace": "Production",
				"polarismesh.cn/overideService":   "demo.prod",
			},
		},
	}

	name, namespace := getPolarisName(service)
	if name != "demo" || namespace != "default" {
		t.Fatalf("got %s/%s, want default/demo", namespace, name)
	}
}

func TestGetServiceAliasReturnsBindingAndOwners(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/naming/v1/service/aliases" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("alias"); got != "order" {
			t.Fatalf("alias query=%q", got)
		}
		if got := r.URL.Query().Get("alias_namespace"); got != "Production" {
			t.Fatalf("alias_namespace query=%q", got)
		}
		return aliasAPIResponse(http.StatusOK,
			`{"code":200000,"info":"success","amount":1,"size":1,"aliases":[{"service":"order","namespace":"default","alias":"order","alias_namespace":"Production","owners":"polaris-controller"}]}`)
	})

	alias, found, err := GetServiceAlias("Production", "order")
	if err != nil {
		t.Fatal(err)
	}
	if !found || alias.Service != "order" || alias.Namespace != "default" || alias.Owners != Source {
		t.Fatalf("unexpected alias: %+v, found=%v", alias, found)
	}
}

func TestUpdateServiceAliasChangesSourceService(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPut || r.URL.Path != "/naming/v1/service/alias" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request CreateServiceAliasRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Service != "order-new" || request.Namespace != "default" {
			t.Fatalf("unexpected source service: %+v", request)
		}
		if request.Owners != Source {
			t.Fatalf("owners=%q", request.Owners)
		}
		return aliasAPIResponse(http.StatusOK,
			`{"code":200002,"info":"no need update","alias":{"service":"order-new","namespace":"default","alias":"order","alias_namespace":"Production","owners":"polaris-controller"}}`)
	})

	service := &v1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default",
		Name:      "order-new",
		Annotations: map[string]string{
			"polarismesh.cn/aliasNamespace": "Production",
			"polarismesh.cn/aliasService":   "order",
		},
	}}
	if _, err := UpdateServiceAlias(service, Alias{Owners: Source}, Source); err != nil {
		t.Fatal(err)
	}
}

func TestCreateServiceAliasDoesNotHideExistedResource(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/naming/v1/namespaces":
			return aliasAPIResponse(http.StatusOK,
				`{"code":200000,"info":"success","responses":[{"code":200000}]}`)
		case "/naming/v1/service/alias":
			return aliasAPIResponse(http.StatusBadRequest,
				`{"code":400201,"info":"resource exists"}`)
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return nil, nil
	})

	service := &v1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default",
		Name:      "order",
		Annotations: map[string]string{
			"polarismesh.cn/aliasNamespace": "Production",
			"polarismesh.cn/aliasService":   "order",
		},
	}}
	if _, err := CreateServiceAlias(service, Source); err == nil || !strings.Contains(err.Error(), "400201") {
		t.Fatalf("ExistedResource was hidden: %v", err)
	}
}

func TestDeleteInstancesRejectsTopLevelUnauthorized(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusUnauthorized,
			`{"code":401000,"info":"unauthorized"}`)
	})

	err := DeleteInstances([]Instance{{Service: "svc", Namespace: "ns", Host: "10.0.0.1", Port: util.IntPtr(80)}},
		10, "svc")
	if err == nil || !strings.Contains(err.Error(), "401000") {
		t.Fatalf("unauthorized delete was treated as success: %v", err)
	}
}

func TestDeleteInstancesChecksHTTP200ResponseBody(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusOK,
			`{"code":401000,"info":"unauthorized"}`)
	})

	err := DeleteInstances([]Instance{{Service: "svc", Namespace: "ns", Host: "10.0.0.1", Port: util.IntPtr(80)}},
		10, "svc")
	if err == nil || !strings.Contains(err.Error(), "401000") {
		t.Fatalf("HTTP 200 error body was treated as success: %v", err)
	}
}

func TestDeleteInstancesDoesNotHidePerInstanceFailure(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusOK,
			`{"code":200000,"info":"success","responses":[{"code":401000,"info":"unauthorized","instance":{"host":"10.0.0.1","port":80}}]}`)
	})

	err := DeleteInstances([]Instance{{Service: "svc", Namespace: "ns", Host: "10.0.0.1", Port: util.IntPtr(80)}},
		10, "svc")
	if err == nil || !strings.Contains(err.Error(), "401000") {
		t.Fatalf("per-instance failure was treated as success: %v", err)
	}
}

func TestUpdateInstancesChecksHTTP200ResponseBody(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusOK,
			`{"code":401000,"info":"unauthorized"}`)
	})

	err := UpdateInstances([]Instance{{Service: "svc", Namespace: "ns", Host: "10.0.0.1", Port: util.IntPtr(80)}},
		10, "svc")
	if err == nil || !strings.Contains(err.Error(), "401000") {
		t.Fatalf("HTTP 200 update failure was treated as success: %v", err)
	}
}

func TestUpdateInstancesDoesNotHidePerInstanceFailure(t *testing.T) {
	newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusOK,
			`{"code":200000,"info":"success","responses":[{"code":401000,"info":"unauthorized","instance":{"host":"10.0.0.1","port":80}}]}`)
	})

	err := UpdateInstances([]Instance{{Service: "svc", Namespace: "ns", Host: "10.0.0.1", Port: util.IntPtr(80)}},
		10, "svc")
	if err == nil || !strings.Contains(err.Error(), "401000") {
		t.Fatalf("per-instance update failure was treated as success: %v", err)
	}
}

type polarisTestRoundTripper func(*http.Request) (*http.Response, error)

func (f polarisTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newPolarisTestClient(t *testing.T, handler polarisTestRoundTripper) {
	t.Helper()
	client, url, token, operator := http.DefaultClient, PolarisHttpURL, PolarisAccessToken, PolarisOperator
	t.Cleanup(func() {
		http.DefaultClient, PolarisHttpURL, PolarisAccessToken, PolarisOperator =
			client, url, token, operator
	})

	http.DefaultClient = &http.Client{Transport: handler}
	PolarisHttpURL = "http://polaris.invalid"
	PolarisAccessToken = "test-token"
	PolarisOperator = ""
}

func aliasAPIResponse(status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

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

func TestDealAddInstanceResponseRejectsUnauthorized(t *testing.T) {
	tests := []struct {
		name     string
		response AddResponse
	}{
		{
			name:     "top level",
			response: AddResponse{Code: 401000, Info: "unauthorized"},
		},
		{
			name: "per instance",
			response: AddResponse{
				Code: 200000,
				Responses: []InstanceResponse{{
					Code:     401000,
					Info:     "unauthorized",
					Instance: SimpleInstance{Host: "10.0.0.1", Port: 80},
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			polarisErrors := NewPErrors()
			dealAddInstanceResponse(test.response, "svc", 0, 1, "request", polarisErrors)
			err := polarisErrors.GetError()
			if err == nil || !strings.Contains(err.Error(), "401000") {
				t.Fatalf("unauthorized add was treated as success: %v", err)
			}
		})
	}
}

func TestDealUpdateInstanceResponseAcceptsNoNeedUpdate(t *testing.T) {
	tests := []struct {
		name     string
		response AddResponse
	}{
		{
			name: "top level without responses",
			response: AddResponse{
				Code: NoNeedUpdate,
				Info: "no need update",
			},
		},
		{
			name: "top level and child",
			response: AddResponse{
				Code: NoNeedUpdate,
				Info: "no need update",
				Responses: []InstanceResponse{{
					Code:     NoNeedUpdate,
					Info:     "no need update",
					Instance: SimpleInstance{Host: "10.0.0.1", Port: 80},
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			polarisErrors := NewPErrors()
			dealUpdateInstanceResponse(test.response, "svc", 0, 1, polarisErrors)
			if err := polarisErrors.GetError(); err != nil {
				t.Fatalf("NoNeedUpdate was returned as a failure: %v", err)
			}
		})
	}
}

func TestDealUpdateInstanceResponseNoNeedUpdateDoesNotHideFailure(t *testing.T) {
	polarisErrors := NewPErrors()
	response := AddResponse{
		Code: NoNeedUpdate,
		Responses: []InstanceResponse{
			{Code: NoNeedUpdate, Instance: SimpleInstance{Host: "10.0.0.1", Port: 80}},
			{
				Code:     500001,
				Info:     "real failure",
				Instance: SimpleInstance{Host: "10.0.0.2", Port: 80},
			},
		},
	}

	dealUpdateInstanceResponse(response, "svc", 0, 1, polarisErrors)
	err := polarisErrors.GetError()
	if err == nil || !strings.Contains(err.Error(), `"code":500001`) {
		t.Fatalf("real child failure was not preserved: %v", err)
	}
}

func TestUpdateInstancesAcceptsTopLevelNoNeedUpdate(t *testing.T) {
	newPolarisTestClient(t, func(*http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusOK,
			`{"code":200002,"info":"no need update","responses":[`+
				`{"code":200002,"info":"no need update","instance":{"host":"10.0.0.1","port":80}}]}`)
	})
	if err := UpdateInstances(
		[]Instance{{Host: "10.0.0.1", Port: util.IntPtr(80)}}, 100, "review"); err != nil {
		t.Fatalf("successful unchanged batch was returned as a failure: %v", err)
	}
}

func TestUpdateInstancesRejectsNoNeedUpdateWithHTTPError(t *testing.T) {
	newPolarisTestClient(t, func(*http.Request) (*http.Response, error) {
		return aliasAPIResponse(http.StatusInternalServerError,
			`{"code":200002,"info":"unexpected response","responses":[]}`)
	})
	err := UpdateInstances(
		[]Instance{{Host: "10.0.0.1", Port: util.IntPtr(80)}}, 100, "review")
	if err == nil {
		t.Fatal("HTTP 500 with NoNeedUpdate body was accepted")
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
