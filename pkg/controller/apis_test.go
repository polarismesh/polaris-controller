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
	"errors"
	"maps"
	"testing"

	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

func TestGetAllInstanceReturnsReadError(t *testing.T) {
	want := errors.New("discovery unavailable")
	p := &PolarisController{consumer: &testConsumer{err: want}}
	instances, err := p.getAllInstance(testService())
	if !errors.Is(err, want) || instances != nil {
		t.Fatalf("instances=%v error=%v", instances, err)
	}
}

func TestMergeMetadataProtectsOwnership(t *testing.T) {
	for _, annotation := range []string{"", `{"source":"foreign","clusterName":"foreign","tenant":"service"}`, "null"} {
		t.Run(annotation, func(t *testing.T) {
			svc := testService()
			svc.Annotations[util.PolarisMetadata] = annotation
			labels := map[string]string{util.PolarisSource: "foreign", util.PolarisClusterName: "foreign", "tenant": "pod"}
			original := maps.Clone(labels)
			metadata := mergeMetadataWithService(svc, address.Address{Metadata: labels}, "test")
			if metadata[util.PolarisSource] != Source || metadata[util.PolarisClusterName] != "test" || metadata["tenant"] != "pod" {
				t.Fatalf("unexpected merged metadata: %v", metadata)
			}
			if !maps.Equal(labels, original) {
				t.Fatal("mutated Pod labels")
			}
			p, _ := newTestController(t)
			if !p.isSyncInstance(testInstance{metadata: metadata}) {
				t.Fatal("lost instance ownership")
			}
		})
	}
}

func TestCompareInstanceMetadata(t *testing.T) {
	tests := []struct {
		name, annotation string
		pod, current     map[string]string
		want             bool
	}{
		{name: "unchanged empty labels", current: map[string]string{}, want: false},
		{name: "unchanged many labels", pod: map[string]string{"app": "a", "region": "", "tenant": "t"},
			current: map[string]string{"app": "a", "region": "", "tenant": "t"}},
		{name: "changed Pod label", pod: map[string]string{"app": "new"}, current: map[string]string{"app": "old"}, want: true},
		{name: "matching Service metadata does not hide Pod change", annotation: `{"tenant":"t"}`,
			pod: map[string]string{"app": "new"}, current: map[string]string{"tenant": "t", "app": "old"}, want: true},
		{name: "removed Pod label", current: map[string]string{"app": "old"}, want: true},
		{name: "added empty label", pod: map[string]string{"region": ""}, current: map[string]string{}, want: true},
		{name: "removed empty label", current: map[string]string{"region": ""}, want: true},
		{name: "removed Service metadata key", annotation: `{"tenant":"t"}`,
			current: map[string]string{"tenant": "t", "obsolete": "value"}, want: true},
		{name: "removed Service annotation", current: map[string]string{"tenant": "t"}, want: true},
		{name: "Pod takes precedence", annotation: `{"app":"service"}`, pod: map[string]string{"app": "pod"},
			current: map[string]string{"app": "pod"}},
		{name: "preserve legacy defaults", current: map[string]string{"version": "v1", "protocol": "http", "platform": Source}},
		{name: "nil metadata", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestController(t)
			svc := testService()
			svc.Annotations[util.PolarisMetadata] = tt.annotation
			metadata := maps.Clone(tt.current)
			if metadata != nil {
				metadata[util.PolarisSource] = Source
				metadata[util.PolarisClusterName] = "test"
			}
			spec := &address.Address{IP: "10.0.0.1", Port: 80, Healthy: true, Weight: 100, Protocol: "http/TCP", Metadata: tt.pod}
			cur := &address.Address{IP: spec.IP, Port: 80, Weight: 100, Protocol: spec.Protocol, PolarisInstance: testInstance{metadata: metadata}}
			if got := p.compareInstanceUpdate(svc, spec, cur); got != tt.want {
				t.Fatalf("update=%v, want %v", got, tt.want)
			}
			if tt.want {
				add, del, updates := p.CompareInstance(svc, address.InstanceSet{"ip": spec}, address.InstanceSet{"ip": cur})
				if len(add) != 0 || len(del) != 0 || len(updates) != 1 {
					t.Fatalf("unexpected diff: %v %v %v", add, del, updates)
				}
				merged := mergeMetadataWithService(svc, updates[0], "test")
				cur.PolarisInstance = testInstance{metadata: merged}
				if p.compareInstanceUpdate(svc, spec, cur) {
					t.Fatal("metadata update did not converge")
				}
			}
		})
	}
}
