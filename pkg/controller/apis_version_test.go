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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

func TestResolveInstanceVersion(t *testing.T) {
	tests := []struct {
		name       string
		metadata   map[string]string
		podVersion string
		want       string
		managed    bool
	}{
		{name: "custom version only", metadata: map[string]string{util.PolarisCustomVersion: "v3"}, want: "v3", managed: true},
		{name: "pod version only", podVersion: "v2", want: "v2", managed: true},
		{
			name:     "custom version takes precedence",
			metadata: map[string]string{util.PolarisCustomVersion: "v3"}, podVersion: "v2",
			want: "v3", managed: true,
		},
		{
			name:     "ordinary metadata version is not managed",
			metadata: map[string]string{util.PolarisVersion: "v4"},
		},
		{name: "empty custom version falls back to pod", metadata: map[string]string{util.PolarisCustomVersion: ""}, podVersion: "v2",
			want: "v2", managed: true},
		{name: "no version source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, managed := resolveInstanceVersion(tt.metadata, tt.podVersion)
			if got != tt.want || managed != tt.managed {
				t.Fatalf("version=%q managed=%v, want version=%q managed=%v",
					got, managed, tt.want, tt.managed)
			}
		})
	}
}

func TestCompareManagedInstanceVersion(t *testing.T) {
	tests := []struct {
		name            string
		serviceMetadata string
		podLabels       map[string]string
		currentVersion  string
		wantUpdate      bool
	}{
		{
			name: "custom version drift", serviceMetadata: `{"polarismesh.cn/customVersion":"v3"}`,
			currentVersion: "v1", wantUpdate: true,
		},
		{
			name: "custom version unchanged", serviceMetadata: `{"polarismesh.cn/customVersion":"v3"}`,
			currentVersion: "v3",
		},
		{
			name: "pod version drift", podLabels: map[string]string{util.PolarisVersion: "v2"},
			currentVersion: "v1", wantUpdate: true,
		},
		{
			name: "historical empty version", podLabels: map[string]string{util.PolarisVersion: "v2"},
			currentVersion: "", wantUpdate: true,
		},
		{
			name: "custom version overrides pod", serviceMetadata: `{"polarismesh.cn/customVersion":"v3"}`,
			podLabels: map[string]string{util.PolarisVersion: "v2"}, currentVersion: "v1", wantUpdate: true,
		},
		{
			name: "custom version overrides pod unchanged", serviceMetadata: `{"polarismesh.cn/customVersion":"v3"}`,
			podLabels: map[string]string{util.PolarisVersion: "v2"}, currentVersion: "v3",
		},
		{
			name: "ordinary metadata version is unmanaged", serviceMetadata: `{"version":"v4"}`,
			currentVersion: "v9",
		},
		{name: "no source preserves manual version", currentVersion: "v9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestController(t)
			service := testService()
			service.Annotations[util.PolarisMetadata] = tt.serviceMetadata
			spec := &address.Address{
				IP: "10.0.0.1", Port: 80, Healthy: true, Weight: util.DefaultWeight,
				Protocol: string(v1.ProtocolTCP), Metadata: tt.podLabels,
			}
			spec.Version = tt.podLabels[util.PolarisVersion]
			metadata := mergeMetadataWithService(service, *spec, p.config.PolarisController.ClusterName)
			current := &address.Address{
				IP: spec.IP, Port: spec.Port, Healthy: spec.Healthy, Weight: spec.Weight, Protocol: spec.Protocol,
				PolarisInstance: testInstance{
					metadata: metadata, weight: spec.Weight, protocol: spec.Protocol, version: tt.currentVersion,
				},
			}
			if got := p.compareInstanceUpdate(service, desiredHealthCheck{}, spec, current); got != tt.wantUpdate {
				t.Fatalf("update=%v, want %v", got, tt.wantUpdate)
			}
		})
	}
}

func TestSyncInstanceReconcilesManagedVersion(t *testing.T) {
	for _, initialVersion := range []string{"v1", ""} {
		t.Run("current="+initialVersion, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			addVersionTestEndpoint(t, stores, "10.0.0.1", map[string]string{util.PolarisVersion: "v2"})

			current := ownedInstance("10.0.0.1")
			current.metadata[util.PolarisVersion] = "v2"
			current.version = initialVersion
			p.consumer.(*testConsumer).instances = []model.Instance{current}

			if err := p.processSyncInstance(testService()); err != nil {
				t.Fatal(err)
			}
			payload := assertVersionRequest(t, tr, http.MethodPut)
			if got := requestVersion(t, payload); got != "v2" {
				t.Fatalf("version=%q, want v2", got)
			}

			current.version = "v2"
			p.consumer.(*testConsumer).instances = []model.Instance{current}
			resetRequests(tr)
			if err := p.processSyncInstance(testService()); err != nil {
				t.Fatal(err)
			}
			if len(tr.requests) != 0 {
				t.Fatalf("version update did not converge: %+v", tr.requests)
			}
		})
	}
}

func TestSyncInstanceReconcilesCustomVersionBeforePodVersion(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.Annotations[util.PolarisMetadata] = `{"polarismesh.cn/customVersion":"v3"}`
	addVersionTestEndpoint(t, stores, "10.0.0.1", map[string]string{util.PolarisVersion: "v2"})

	current := ownedInstance("10.0.0.1")
	current.metadata[util.PolarisCustomVersion] = "v3"
	current.metadata[util.PolarisVersion] = "v2"
	current.version = "v1"
	p.consumer.(*testConsumer).instances = []model.Instance{current}

	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	payload := assertVersionRequest(t, tr, http.MethodPut)
	if got := requestVersion(t, payload); got != "v3" {
		t.Fatalf("version=%q, want custom version v3", got)
	}

	current.version = "v3"
	p.consumer.(*testConsumer).instances = []model.Instance{current}
	resetRequests(tr)
	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("custom version update did not converge: %+v", tr.requests)
	}
}

func TestSyncInstancePreservesUnmanagedVersion(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addVersionTestEndpoint(t, stores, "10.0.0.1", nil)

	current := ownedInstance("10.0.0.1")
	current.version = "v9"
	p.consumer.(*testConsumer).instances = []model.Instance{current}

	if err := p.processSyncInstance(testService()); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("unmanaged version caused writes: %+v", tr.requests)
	}

	service := testService()
	service.Annotations[util.PolarisWeight] = "80"
	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	payload := assertVersionRequest(t, tr, http.MethodPut)
	if _, exists := payload["version"]; exists {
		t.Fatalf("unmanaged version was included in update: %s", tr.requests[0].body)
	}

	current.weight = 80
	p.consumer.(*testConsumer).instances = []model.Instance{current}
	resetRequests(tr)
	if err := p.processSyncInstance(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("unmanaged version update did not converge: %+v", tr.requests)
	}
}

func TestSyncInstanceStopsManagingRemovedVersionSource(t *testing.T) {
	t.Run("pod version removed", func(t *testing.T) {
		p, stores := newTestController(t)
		tr := mockPolarisHTTP(t)
		addVersionTestEndpoint(t, stores, "10.0.0.1", nil)

		current := ownedInstance("10.0.0.1")
		current.metadata[util.PolarisVersion] = "v2"
		current.version = "v2"
		p.consumer.(*testConsumer).instances = []model.Instance{current}

		if err := p.processSyncInstance(testService()); err != nil {
			t.Fatal(err)
		}
		if len(tr.requests) != 0 {
			t.Fatalf("removed Pod version triggered writes: %+v", tr.requests)
		}
	})

	t.Run("custom version removed", func(t *testing.T) {
		p, stores := newTestController(t)
		tr := mockPolarisHTTP(t)
		addVersionTestEndpoint(t, stores, "10.0.0.1", nil)

		current := ownedInstance("10.0.0.1")
		current.metadata[util.PolarisCustomVersion] = "v3"
		current.version = "v3"
		p.consumer.(*testConsumer).instances = []model.Instance{current}

		if err := p.processSyncInstance(testService()); err != nil {
			t.Fatal(err)
		}
		payload := assertVersionRequest(t, tr, http.MethodPut)
		if _, exists := payload["version"]; exists {
			t.Fatalf("removed custom version was cleared in update: %s", tr.requests[0].body)
		}

		current.metadata = map[string]string{
			util.PolarisSource:       Source,
			util.PolarisClusterName:  "test",
			util.K8sServiceNamespace: "ns",
			util.K8sServiceName:      "svc",
			util.K8sServiceUID:       "",
		}
		p.consumer.(*testConsumer).instances = []model.Instance{current}
		resetRequests(tr)
		if err := p.processSyncInstance(testService()); err != nil {
			t.Fatal(err)
		}
		if len(tr.requests) != 0 {
			t.Fatalf("removed custom version did not converge: %+v", tr.requests)
		}
	})
}

func addVersionTestEndpoint(t *testing.T, stores testStores, ip string, labels map[string]string) {
	t.Helper()
	addTestEndpoint(t, stores, ip)
	storeObject(t, stores.pods, &v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "ns", Name: "pod", Labels: labels,
	}})
}

func assertVersionRequest(t *testing.T, tr *testTransport, method string) map[string]json.RawMessage {
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
	return payload[0]
}

func requestVersion(t *testing.T, payload map[string]json.RawMessage) string {
	t.Helper()
	raw, exists := payload["version"]
	if !exists {
		t.Fatal("version field is missing")
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		t.Fatal(err)
	}
	return version
}

func resetRequests(tr *testTransport) {
	tr.Lock()
	defer tr.Unlock()
	tr.requests = nil
}
