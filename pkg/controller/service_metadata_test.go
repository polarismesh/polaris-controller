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

package controller

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
)

func TestUpdateServiceLabelChanges(t *testing.T) {
	tests := []struct {
		name          string
		oldLabels     map[string]string
		currentLabels map[string]string
		remote        map[string]string
		want          map[string]string
		wantWrite     bool
	}{
		{
			name:          "add missing label",
			oldLabels:     map[string]string{"manual": "keep"},
			currentLabels: map[string]string{"manual": "keep", "tenant": "t1"},
			remote:        map[string]string{"manual": "keep", "remote-only": "keep"},
			want:          map[string]string{"manual": "keep", "tenant": "t1", "remote-only": "keep"},
			wantWrite:     true,
		},
		{
			name:          "overwrite changed label",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: map[string]string{"env": "v2"},
			remote:        map[string]string{"env": "v1", "manual": "keep"},
			want:          map[string]string{"env": "v2", "manual": "keep"},
			wantWrite:     true,
		},
		{
			name:          "preserve unchanged label",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: map[string]string{"env": "v1"},
			remote:        map[string]string{"env": "manual", "manual": "keep"},
			want:          map[string]string{"env": "manual", "manual": "keep"},
		},
		{
			name:          "added label replaces remote value",
			currentLabels: map[string]string{"env": "v1"},
			remote:        map[string]string{"env": "manual", "manual": "keep"},
			want:          map[string]string{"env": "v1", "manual": "keep"},
			wantWrite:     true,
		},
		{
			name:          "changed label already matches remote",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: map[string]string{"env": "v2"},
			remote:        map[string]string{"env": "v2", "manual": "keep"},
			want:          map[string]string{"env": "v2", "manual": "keep"},
		},
		{
			name:          "delete removed label when value matches",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: nil,
			remote:        map[string]string{"env": "v1", "manual": "keep"},
			want:          map[string]string{"manual": "keep"},
			wantWrite:     true,
		},
		{
			name:          "preserve removed label when remote value drifted",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: nil,
			remote:        map[string]string{"env": "v2", "manual": "keep"},
			want:          map[string]string{"env": "v2", "manual": "keep"},
		},
		{
			name:      "preserve remote only metadata",
			remote:    map[string]string{"manual": "keep"},
			want:      map[string]string{"manual": "keep"},
			wantWrite: false,
		},
		{
			name:          "initialize nil remote metadata",
			currentLabels: map[string]string{"env": "v1"},
			want:          map[string]string{"env": "v1"},
			wantWrite:     true,
		},
		{
			name:          "removed label already absent remotely",
			oldLabels:     map[string]string{"env": "v1"},
			currentLabels: nil,
			remote:        map[string]string{"manual": "keep"},
			want:          map[string]string{"manual": "keep"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, _ := newTestController(t)
			tr := mockPolarisHTTP(t)
			tr.respond = func(request *http.Request) (*http.Response, error) {
				switch request.Method {
				case http.MethodGet:
					return serviceMetadataResponse(t, test.remote), nil
				case http.MethodPut:
					return successResponse(), nil
				default:
					t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
					return nil, nil
				}
			}

			oldService := testService()
			oldService.Labels = maps.Clone(test.oldLabels)
			currentService := testService()
			currentService.Labels = maps.Clone(test.currentLabels)

			if err := p.updateService(oldService, currentService); err != nil {
				t.Fatal(err)
			}

			var writes []capturedRequest
			for _, request := range tr.requests {
				if request.method == http.MethodPut {
					writes = append(writes, request)
				}
			}
			if !test.wantWrite {
				if len(writes) != 0 {
					t.Fatalf("unexpected UpdateService requests: %d", len(writes))
				}
				return
			}
			if len(writes) != 1 {
				t.Fatalf("UpdateService requests=%d want=1", len(writes))
			}

			var request []polarisapi.Service
			if err := json.Unmarshal(writes[0].body, &request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if len(request) != 1 || !maps.Equal(request[0].Metadata, test.want) {
				t.Fatalf("metadata=%v want=%v", request[0].Metadata, test.want)
			}
		})
	}
}

func TestUpdateServiceReturnsWriteFailure(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			return serviceMetadataResponse(t, map[string]string{"env": "v1"}), nil
		case http.MethodPut:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"code":500000,"info":"write failed"}`)),
			}, nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	}

	oldService := testService()
	oldService.Labels = map[string]string{"env": "v1"}
	currentService := testService()
	currentService.Labels = map[string]string{"env": "v2"}

	if err := p.updateService(oldService, currentService); err == nil {
		t.Fatal("metadata write failure was ignored")
	}
}

func TestUpdateServiceSendsEmptyMetadataWhenLabelsAreRemoved(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return serviceMetadataResponse(t, map[string]string{"app": "foo"}), nil
		}
		return successResponse(), nil
	}
	oldService := testService()
	oldService.Labels = map[string]string{"app": "foo"}
	currentService := testService()

	if err := p.updateService(oldService, currentService); err != nil {
		t.Fatal(err)
	}
	var writes []capturedRequest
	for _, request := range tr.requests {
		if request.method == http.MethodPut {
			writes = append(writes, request)
		}
	}
	if len(writes) != 1 {
		t.Fatalf("UpdateService writes=%d, want 1", len(writes))
	}
	var payload []polarisapi.Service
	if err := json.Unmarshal(writes[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Metadata == nil || len(payload[0].Metadata) != 0 {
		t.Fatalf("empty metadata was omitted: %s", writes[0].body)
	}
}

func TestFirstSyncUsesPersistedLabelBaseline(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = "uid-label"
	service.Labels = map[string]string{"env": "v2"}
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")

	baseline := newServiceSyncState(service, "test", syncStateActive)
	baseline.AppliedLabels = map[string]string{"env": "v1", "retired": "old"}
	if _, err := p.syncStateStore.Upsert(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	remote := map[string]string{"env": "v1", "retired": "old", "manual": "keep"}
	tr.respond = func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/naming/v1/services":
			return serviceMetadataResponse(t, remote), nil
		case request.Method == http.MethodPut && request.URL.Path == "/naming/v1/services":
			var payload []polarisapi.Service
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			if len(payload) == 1 {
				remote = payload[0].Metadata
			}
		}
		return successResponse(), nil
	}

	task := &Task{Namespace: "ns", Name: "svc", ObjectType: KubernetesService, Operation: OperationAdd}
	if err := p.syncService(task); err != nil {
		t.Fatal(err)
	}
	_, retiredExists := remote["retired"]
	if remote["env"] != "v2" || retiredExists {
		t.Fatalf("persisted Label baseline was not reconciled: %+v", remote)
	}
	if remote["manual"] != "keep" {
		t.Fatalf("external metadata was overwritten: %+v", remote)
	}
	record, found, err := p.syncStateStore.Get(context.Background(), baseline.key())
	_, retiredApplied := record.AppliedLabels["retired"]
	if err != nil || !found || record.AppliedLabels["env"] != "v2" || retiredApplied {
		t.Fatalf("applied Labels were not persisted: found=%v record=%+v error=%v", found, record, err)
	}
}

func TestProcessUpdateServiceUsesCachedLabels(t *testing.T) {
	p, stores := newTestController(t)
	service := testService()
	service.Labels = map[string]string{"env": "v2"}
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	p.consumer.(*testConsumer).instances = []model.Instance{ownedInstance("10.0.0.1")}

	tr := mockPolarisHTTP(t)
	tr.respond = func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return serviceMetadataResponse(t, map[string]string{"env": "v1"}), nil
		}
		return successResponse(), nil
	}

	oldService := testService()
	oldService.Labels = map[string]string{"env": "v1"}
	if err := p.processUpdateService(oldService, service); err != nil {
		t.Fatal(err)
	}

	var serviceWrites []capturedRequest
	for _, request := range tr.requests {
		if request.method == http.MethodPut && request.path == "/naming/v1/services" {
			serviceWrites = append(serviceWrites, request)
		}
	}
	if len(serviceWrites) != 1 {
		t.Fatalf("Service metadata writes=%d want=1", len(serviceWrites))
	}
	var request []polarisapi.Service
	if err := json.Unmarshal(serviceWrites[0].body, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(request) != 1 || request[0].Metadata["env"] != "v2" {
		t.Fatalf("metadata=%v want env=v2", request[0].Metadata)
	}
}

func serviceMetadataResponse(t *testing.T, metadata map[string]string) *http.Response {
	t.Helper()
	body, err := json.Marshal(polarisapi.GetServiceResponse{
		Code: polarisapi.ExecuteSuccess,
		Services: []polarisapi.Service{{
			Name:      "svc",
			Namespace: "ns",
			Metadata:  metadata,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}
