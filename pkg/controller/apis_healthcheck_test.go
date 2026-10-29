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

package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

func TestResolveHealthCheck(t *testing.T) {
	tests := []struct {
		name       string
		annotation *string
		want       desiredHealthCheck
		wantErr    bool
	}{
		{name: "unmanaged"},
		{name: "disabled", annotation: util.StringPtr("0"), want: desiredHealthCheck{managed: true}},
		{name: "enabled", annotation: util.StringPtr("10"), want: desiredHealthCheck{managed: true, enabled: true, ttl: 10}},
		{name: "maximum", annotation: util.StringPtr("60"), want: desiredHealthCheck{managed: true, enabled: true, ttl: 60}},
		{name: "trim spaces", annotation: util.StringPtr(" 5 "), want: desiredHealthCheck{managed: true, enabled: true, ttl: 5}},
		{name: "negative", annotation: util.StringPtr("-1"), wantErr: true},
		{name: "above maximum", annotation: util.StringPtr("61"), wantErr: true},
		{name: "not a number", annotation: util.StringPtr("abc"), wantErr: true},
		{name: "empty", annotation: util.StringPtr(""), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := testService()
			if tt.annotation != nil {
				service.Annotations[util.PolarisHeartBeatTTL] = *tt.annotation
			}
			got, err := resolveHealthCheck(service)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected invalid health check config")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("desired=%+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInstanceWritesHealthCheckModes(t *testing.T) {
	tests := []struct {
		name             string
		annotation       *string
		wantEnable       bool
		wantEnableExists bool
		wantHealthCheck  bool
		wantTTL          int
	}{
		{name: "unmanaged"},
		{name: "disabled", annotation: util.StringPtr("0"), wantEnableExists: true},
		{
			name: "enabled", annotation: util.StringPtr("10"),
			wantEnable: true, wantEnableExists: true, wantHealthCheck: true, wantTTL: 10,
		},
	}

	for _, tt := range tests {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				p, _ := newTestController(t)
				tr := mockPolarisHTTP(t)
				service := testService()
				if tt.annotation != nil {
					service.Annotations[util.PolarisHeartBeatTTL] = *tt.annotation
				}
				healthCheck, err := resolveHealthCheck(service)
				if err != nil {
					t.Fatal(err)
				}
				instances := []address.Address{{
					IP: "10.0.0.1", Port: 8080, Healthy: true, Weight: util.DefaultWeight, Protocol: "TCP",
				}}
				if method == http.MethodPost {
					err = p.addInstances(service, healthCheck, instances)
				} else {
					err = p.updateInstances(service, healthCheck, instances)
				}
				if err != nil {
					t.Fatal(err)
				}
				assertHealthCheckRequest(t, tr, method, tt.wantEnable,
					tt.wantEnableExists, tt.wantHealthCheck, tt.wantTTL)
			})
		}
	}
}

func TestHealthCheckComparison(t *testing.T) {
	tests := []struct {
		name           string
		desired        desiredHealthCheck
		currentEnabled bool
		currentTTL     int64
		wantUpdate     bool
	}{
		{name: "unmanaged preserves enabled", currentEnabled: true, currentTTL: 10},
		{name: "unmanaged preserves disabled"},
		{
			name: "disable enabled instance", desired: desiredHealthCheck{managed: true},
			currentEnabled: true, currentTTL: 10, wantUpdate: true,
		},
		{
			name:    "enable disabled instance",
			desired: desiredHealthCheck{managed: true, enabled: true, ttl: 10}, wantUpdate: true,
		},
		{
			name:           "same enabled config",
			desired:        desiredHealthCheck{managed: true, enabled: true, ttl: 10},
			currentEnabled: true, currentTTL: 10,
		},
		{
			name:           "change TTL",
			desired:        desiredHealthCheck{managed: true, enabled: true, ttl: 20},
			currentEnabled: true, currentTTL: 10, wantUpdate: true,
		},
		{
			name:           "enabled instance has zero TTL",
			desired:        desiredHealthCheck{managed: true, enabled: true, ttl: 10},
			currentEnabled: true, wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestController(t)
			service := testService()
			spec := &address.Address{
				IP: "10.0.0.1", Port: 80, Healthy: true, Weight: util.DefaultWeight, Protocol: "TCP",
			}
			instance := ownedInstance(spec.IP)
			instance.enableHealthCheck = tt.currentEnabled
			instance.ttl = tt.currentTTL
			current := &address.Address{
				IP: spec.IP, Port: spec.Port, Healthy: true, Weight: spec.Weight, Protocol: spec.Protocol,
				PolarisInstance: instance,
			}
			if got := p.compareInstanceUpdate(service, tt.desired, spec, current); got != tt.wantUpdate {
				t.Fatalf("update=%v, want %v", got, tt.wantUpdate)
			}
		})
	}
}

func TestSyncInstancePreservesUnmanagedHealthCheck(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")

	instance := ownedInstance("10.0.0.1")
	instance.enableHealthCheck = true
	instance.ttl = 10
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	if err := p.processSyncInstance(testService()); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("unmanaged health check caused writes: %+v", tr.requests)
	}
}

func TestSyncInstanceUpdatesTTLAndConverges(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	service := testService()
	service.Annotations[util.PolarisHeartBeatTTL] = "10"

	instance := ownedInstance("10.0.0.1")
	instance.enableHealthCheck = true
	instance.ttl = 5
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	assertHealthCheckRequest(t, tr, http.MethodPut, true, true, true, 10)

	instance.ttl = 10
	p.consumer.(*testConsumer).instances = []model.Instance{instance}
	tr.Lock()
	tr.requests = nil
	tr.Unlock()
	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("TTL update did not converge: %+v", tr.requests)
	}
}

func TestSyncInstanceExplicitZeroDisablesHealthCheck(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	service := testService()
	service.Annotations[util.PolarisHeartBeatTTL] = "0"

	instance := ownedInstance("10.0.0.1")
	instance.enableHealthCheck = true
	instance.ttl = 10
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	assertHealthCheckRequest(t, tr, http.MethodPut, false, true, false, 0)

	instance.enableHealthCheck = false
	instance.ttl = 0
	p.consumer.(*testConsumer).instances = []model.Instance{instance}
	tr.Lock()
	tr.requests = nil
	tr.Unlock()
	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("health check disable did not converge: %+v", tr.requests)
	}
}

func TestSyncInstanceInvalidTTLDoesNotWrite(t *testing.T) {
	for _, annotation := range []string{"", "abc", "-1", "61"} {
		t.Run(annotation, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			addTestEndpoint(t, stores, "10.0.0.1")
			service := testService()
			service.Annotations[util.PolarisHeartBeatTTL] = annotation
			p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}

			if err := p.processSyncInstance(service); err == nil {
				t.Fatal("expected invalid TTL error")
			}
			if len(tr.requests) != 0 {
				t.Fatalf("invalid TTL caused writes: %+v", tr.requests)
			}
		})
	}
}

func assertHealthCheckRequest(t *testing.T, tr *testTransport, method string, wantEnable bool,
	wantEnableExists bool, wantHealthCheck bool, wantTTL int) {
	t.Helper()
	if len(tr.requests) != 1 {
		t.Fatalf("requests=%+v, want exactly one", tr.requests)
	}
	request := tr.requests[0]
	if request.method != method || request.path != "/naming/v1/instances" {
		t.Fatalf("unexpected request: %s %s", request.method, request.path)
	}

	var payload []map[string]json.RawMessage
	if err := json.Unmarshal(request.body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 {
		t.Fatalf("instances=%d, want 1", len(payload))
	}

	rawEnable, hasEnable := payload[0]["enableHealthCheck"]
	if hasEnable != wantEnableExists {
		t.Fatalf("enableHealthCheck present=%v, want %v; body=%s", hasEnable, wantEnableExists, request.body)
	}
	if hasEnable {
		var enabled bool
		if err := json.Unmarshal(rawEnable, &enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != wantEnable {
			t.Fatalf("enableHealthCheck=%v, want %v", enabled, wantEnable)
		}
	}

	rawHealthCheck, hasHealthCheck := payload[0]["healthCheck"]
	if hasHealthCheck != wantHealthCheck {
		t.Fatalf("healthCheck present=%v, want %v; body=%s", hasHealthCheck, wantHealthCheck, request.body)
	}
	if !hasHealthCheck {
		return
	}

	var healthCheck polarisapi.HealthCheck
	if err := json.Unmarshal(rawHealthCheck, &healthCheck); err != nil {
		t.Fatal(err)
	}
	if healthCheck.Type == nil || *healthCheck.Type != 0 {
		t.Fatalf("healthCheck.type=%v, want HEARTBEAT", healthCheck.Type)
	}
	if healthCheck.Heartbeat.TTL != wantTTL {
		t.Fatalf("healthCheck.ttl=%d, want %d", healthCheck.Heartbeat.TTL, wantTTL)
	}
}
