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
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestInstanceQueryAcceptsZeroPortAndRejectsNegativePort(t *testing.T) {
	for _, port := range []int{0, -1, 65536} {
		t.Run(strconv.Itoa(port), func(t *testing.T) {
			newPolarisTestClient(t, func(*http.Request) (*http.Response, error) {
				instance := queryTestInstance(2)
				instance.Port = util.IntPtr(port)
				return queryPageResponse(t, 1, []Instance{instance}), nil
			})
			result, err := GetInstances(context.Background(), queryTestService())
			if port == 0 && (err != nil || len(result) != 1) || port != 0 && (err == nil || result != nil) {
				t.Fatalf("port=%d result=%v error=%v", port, result, err)
			}
		})
	}
}

func TestDeleteConflictRetainsBusinessCodeAndMixedFailures(t *testing.T) {
	for _, tc := range []struct {
		body      string
		conflict  bool
		code      string
		requested int
	}{
		{`{"code":400208,"info":"the same instance request"}`, true, "400208", 0},
		{`{"code":200000,"responses":[{"code":400208}]}`, true, "400208", 1},
		{`{"code":400000,"responses":[{"code":200000},{"code":400208}]}`, true, "400208", 2},
		{`{"code":400000,"responses":[{"code":400208},{"code":401000,"info":"unauthorized"}]}`, false, "401000", 2},
		{`{"code":400000,"responses":[{"code":400208}]}`, false, "400000", 2},
		{`{"code":400208,"responses":[{"code":401000,"info":"unauthorized"}]}`, false, "401000", 1},
		{`{"code":200000,"responses":[{"code":400208},{"code":500123,"info":"storage failure"}]}`, false, "500123", 2},
		{`{"code":500123,"info":"storage failure"}`, false, "500123", 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var response AddResponse
			if err := json.Unmarshal([]byte(tc.body), &response); err != nil {
				t.Fatal(err)
			}
			failures := NewPErrors()
			dealDeleteInstanceResponse(response, "ns/svc", 0, 1, "test", 0, failures, tc.requested)
			err := failures.GetError()
			if err == nil || errors.Is(err, ErrDeleteConflict) != tc.conflict || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("wrong delete classification: error=%v conflict=%v", err, tc.conflict)
			}
		})
	}
}

func TestNamingRequestsHaveDefaultDeadline(t *testing.T) {
	for _, operation := range []string{"add", "update", "delete", "health", "get service", "create service", "update service", "alias query", "alias create", "alias update", "namespace"} {
		t.Run(operation, func(t *testing.T) {
			newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > polarisRequestTimeout || time.Until(deadline) <= 0 {
					t.Errorf("request has no bounded deadline: %s %s", r.Method, r.URL.Path)
				}
				return nil, errors.New("injected request failure")
			})
			instance := []Instance{queryTestInstance(1)}
			service := queryTestService()
			service.Annotations = map[string]string{util.PolarisAliasService: "alias", util.PolarisAliasNamespace: "ns"}
			switch operation {
			case "add":
				_ = AddInstances(instance, 100, "test")
			case "update":
				_ = UpdateInstances(instance, 100, "test")
			case "delete":
				_ = DeleteInstances(instance, 100, "test")
			case "health":
				_ = CheckHealth()
			case "get service":
				_, _ = GetService(service)
			case "create service":
				_, _ = CreateService(service)
			case "update service":
				_, _, _ = UpdateService(service, nil)
			case "alias query":
				_, _, _ = GetServiceAlias("ns", "alias")
			case "alias create":
				_, _ = CreateServiceAlias(service, "test")
			case "alias update":
				_, _ = UpdateServiceAlias(service, Alias{}, "test")
			case "namespace":
				_, _ = CreateNamespaces("ns")
			}
		})
	}
}

func TestNamingHTTPDeadlineCoversHeadersBodyAndOperatorToken(t *testing.T) {
	for _, stage := range []string{"response headers", "response body", "operator token"} {
		t.Run(stage, func(t *testing.T) {
			entered := make(chan struct{})
			handlerDone := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				if stage == "operator token" && r.URL.Path != "/core/v1/user/token" {
					t.Error("write happened before token lookup completed")
				}
				if stage == "response body" {
					w.WriteHeader(http.StatusOK)
					if _, err := w.Write([]byte(`{"code":`)); err != nil {
						return
					}
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(func() { close(release); server.Close() })
			oldClient, oldURL, oldOperator := http.DefaultClient, PolarisHttpURL, PolarisOperator
			http.DefaultClient, PolarisHttpURL, PolarisOperator = server.Client(), server.URL, ""
			if stage == "operator token" {
				PolarisOperator = "operator"
			}
			t.Cleanup(func() { http.DefaultClient, PolarisHttpURL, PolarisOperator = oldClient, oldURL, oldOperator })
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := DeleteInstancesWithContext(ctx, []Instance{queryTestInstance(1)}, 100, "bounded request")
			if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatalf("stalled %s did not honor context: time=%v error=%v", stage, time.Since(start), err)
			}
			select {
			case <-entered:
			default:
				t.Fatal("fixture never entered handler")
			}
			select {
			case <-handlerDone:
			case <-time.After(time.Second):
				t.Fatal("HTTP handler did not observe cancellation")
			}
		})
	}
}

func TestCreateServiceReportsCreatedOnlyOnConfirmedSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		status          int
		created, failed bool
	}{
		{"new", `{"code":200000,"responses":[{"code":200000}]}`, 200, true, false},
		{"top exists", `{"code":400201}`, 400, false, false},
		{"item exists", `{"code":200000,"responses":[{"code":400201}]}`, 200, false, false},
		{"item failure", `{"code":200000,"responses":[{"code":401000}]}`, 200, false, true},
		{"top failure", `{"code":401000}`, 200, false, true},
		{"top failure with exists item", `{"code":401000,"responses":[{"code":400201}]}`, 200, false, true},
		{"HTTP failure with exists item", `{"code":200000,"responses":[{"code":400201}]}`, 500, false, true},
		{"HTTP failure", `{"code":200000}`, 500, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newPolarisTestClient(t, func(*http.Request) (*http.Response, error) { return aliasAPIResponse(tc.status, tc.body) })
			response, err := CreateService(queryTestService())
			if (err != nil) != tc.failed || response.Created != tc.created {
				t.Fatalf("created=%v error=%v", response.Created, err)
			}
		})
	}
}

func TestFailureSignatureTracksBusinessCodesAcrossErrorWrapping(t *testing.T) {
	for _, operation := range []string{"get service", "create service", "update service", "alias query", "alias create", "alias update"} {
		t.Run(operation, func(t *testing.T) {
			service := queryTestService()
			service.Annotations = map[string]string{util.PolarisAliasService: "alias", util.PolarisAliasNamespace: "ns"}
			for _, code := range []uint32{401000, 500123} {
				newPolarisTestClient(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == createNamespace {
						return aliasAPIResponse(200, `{"code":200000}`)
					}
					return aliasAPIResponse(400, fmt.Sprintf(`{"code":%d,"info":"injected failure"}`, code))
				})
				var err error
				switch operation {
				case "get service":
					_, err = GetService(service)
				case "create service":
					_, err = CreateService(service)
				case "update service":
					_, _, err = UpdateService(service, nil)
				case "alias query":
					_, _, err = GetServiceAlias("ns", "alias")
				case "alias create":
					_, err = CreateServiceAlias(service, "test")
				case "alias update":
					_, err = UpdateServiceAlias(service, Alias{}, "test")
				}
				if err == nil || FailureSignature(fmt.Errorf("service wrapper: %w", err)) != fmt.Sprint(code) {
					t.Fatalf("failure category lost: code=%d error=%v signature=%s", code, err, FailureSignature(err))
				}
			}
		})
	}
	sdk := model.NewSDKErrorWithServerInfo(model.ErrCodeServerException, nil, 401000, "unauthorized", "request failed")
	if signature := FailureSignature(fmt.Errorf("SDK wrapper: %w", sdk)); !strings.Contains(signature, "401000") || !strings.Contains(signature, "sdk-") {
		t.Fatalf("SDK error category was lost: %s", signature)
	}
	mixed := errors.Join(&BatchError{Errors: []PError{{Code: util.Uint32Ptr(SameInstanceRequest)}}},
		&APIError{Code: 401000, Info: "unauthorized"})
	if signature := FailureSignature(mixed); signature != "400208,401000" {
		t.Fatalf("mixed error was misclassified as conflict alone: %s", signature)
	}
}
