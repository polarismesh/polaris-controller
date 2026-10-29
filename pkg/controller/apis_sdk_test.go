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
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
)

type sdkTestInstance struct {
	testInstance
	port uint32
}

func (i sdkTestInstance) GetPort() uint32           { return i.port }
func (i sdkTestInstance) GetProtocol() string       { return "grpc" }
func (i sdkTestInstance) IsEnableHealthCheck() bool { return true }
func (i sdkTestInstance) IsHealthy() bool           { return false }
func (i sdkTestInstance) IsIsolated() bool          { return true }

func TestSyncExistingSDKInstancesDoesNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata map[string]string
	}{
		{name: "SDK", metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "test"}},
		{name: "Java Agent", metadata: map[string]string{util.PolarisSource: "polaris-java-agent", util.PolarisClusterName: "test"}},
		{name: "no metadata"},
		{name: "no cluster", metadata: map[string]string{util.PolarisSource: "sdk"}},
		{name: "empty source", metadata: map[string]string{util.PolarisClusterName: "test"}},
		{name: "foreign SDK", metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "other"}},
		{name: "foreign controller", metadata: map[string]string{util.PolarisSource: Source, util.PolarisClusterName: "other"}},
	} {
		for _, notReady := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/notReady=%t", tc.name, notReady), func(t *testing.T) {
				p, stores := newTestController(t)
				tr := mockPolarisHTTP(t)
				addTestEndpoint(t, stores, "10.0.0.1")
				if notReady {
					ep, err := p.endpointsLister.Endpoints("ns").Get("svc")
					if err != nil {
						t.Fatal(err)
					}
					ep = ep.DeepCopy()
					ep.Subsets[0].NotReadyAddresses = ep.Subsets[0].Addresses
					ep.Subsets[0].Addresses = nil
					storeObject(t, stores.endpoints, ep)
				}
				instance := sdkTestInstance{testInstance: testInstance{
					host: "10.0.0.1", weight: 37, metadata: maps.Clone(tc.metadata),
				}, port: 80}
				p.consumer.(*testConsumer).instances = []model.Instance{instance}
				for round := 0; round < 3; round++ {
					if err := p.processSyncInstance(testService()); err != nil {
						t.Fatal(err)
					}
				}
				if len(tr.requests) != 0 {
					t.Fatalf("existing instance caused writes across repeated syncs: %+v", tr.requests)
				}
				if !maps.Equal(instance.metadata, tc.metadata) {
					t.Fatalf("SDK metadata was mutated: %v", instance.metadata)
				}
			})
		}
	}
}

func TestSyncInstanceDeletionPreservesOwnership(t *testing.T) {
	for _, tc := range []struct {
		name       string
		metadata   map[string]string
		wantDelete bool
	}{
		{name: "controller in current cluster", metadata: map[string]string{util.PolarisSource: Source, util.PolarisClusterName: "test"}, wantDelete: true},
		{name: "legacy controller in current cluster", metadata: map[string]string{util.PolarisOldSource: Source, util.PolarisClusterName: "test"}, wantDelete: true},
		{name: "SDK in current cluster", metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "test"}},
		{name: "Java Agent in current cluster", metadata: map[string]string{util.PolarisSource: "polaris-java-agent", util.PolarisClusterName: "test"}},
		{name: "no source in current cluster", metadata: map[string]string{util.PolarisClusterName: "test"}},
		{name: "no metadata"},
		{name: "no cluster", metadata: map[string]string{util.PolarisSource: "sdk"}},
		{name: "foreign SDK", metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "other"}},
		{name: "foreign controller", metadata: map[string]string{util.PolarisSource: Source, util.PolarisClusterName: "other"}},
		{name: "foreign legacy controller", metadata: map[string]string{util.PolarisOldSource: Source, util.PolarisClusterName: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, stores := newTestController(t)
			tr := mockPolarisHTTP(t)
			addTestEndpoint(t, stores)
			p.consumer.(*testConsumer).instances = []model.Instance{sdkTestInstance{
				testInstance: testInstance{host: "10.0.0.1", metadata: tc.metadata}, port: 8080,
			}}
			if err := p.processSyncInstance(testService()); err != nil {
				t.Fatal(err)
			}
			if !tc.wantDelete {
				if len(tr.requests) != 0 {
					t.Fatalf("instance not owned by this controller caused writes: %+v", tr.requests)
				}
				return
			}
			if len(tr.requests) != 1 || tr.requests[0].path != "/naming/v1/instances/delete" ||
				tr.requests[0].method != http.MethodPost {
				t.Fatalf("expected controller-owned instance deletion, got %+v", tr.requests)
			}
			var payload []polarisapi.Instance
			if err := json.Unmarshal(tr.requests[0].body, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 1 || payload[0].Host != "10.0.0.1" || payload[0].Port == nil || *payload[0].Port != 8080 {
				t.Fatalf("unexpected deleted instance: %+v", payload)
			}
		})
	}
}

func TestDeleteServiceDoesNotRemoveInstanceForAnotherServiceUID(t *testing.T) {
	p, _ := newTestController(t)
	tr := mockPolarisHTTP(t)
	service := testService()
	service.UID = "uid-current"
	instance := ownedInstance("10.0.0.1")
	instance.metadata[util.K8sServiceUID] = "uid-old"
	p.consumer.(*testConsumer).instances = []model.Instance{instance}

	if err := p.processDeleteService(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("deleted instance owned by another Service UID: %+v", tr.requests)
	}
}

func TestSyncSDKInstanceUsesHostAndPort(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	addTestEndpoint(t, stores, "10.0.0.1")
	p.consumer.(*testConsumer).instances = []model.Instance{sdkTestInstance{
		testInstance: testInstance{host: "10.0.0.1", metadata: map[string]string{util.PolarisSource: "sdk"}}, port: 8080,
	}}
	if err := p.processSyncInstance(testService()); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 1 || tr.requests[0].method != http.MethodPost || tr.requests[0].path != "/naming/v1/instances" {
		t.Fatalf("expected registration for the missing port, got %+v", tr.requests)
	}
	var payload []polarisapi.Instance
	if err := json.Unmarshal(tr.requests[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload[0].Host != "10.0.0.1" || payload[0].Port == nil || *payload[0].Port != 80 {
		t.Fatalf("unexpected registered instance: %+v", payload)
	}
}

func TestCompareInstanceMixedOwnership(t *testing.T) {
	p, _ := newTestController(t)
	spec := make(address.InstanceSet)
	for _, host := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5", "10.0.0.6", "2001:db8::1"} {
		spec[host+"-80"] = &address.Address{
			IP: host, Port: 80, Healthy: true, Weight: util.DefaultWeight, Protocol: "TCP",
		}
	}
	changed := ownedInstance("10.0.0.2")
	changed.weight = 1
	legacy := ownedInstance("10.0.0.3")
	delete(legacy.metadata, util.PolarisSource)
	legacy.metadata[util.PolarisOldSource] = Source
	foreign := ownedInstance("10.0.0.5")
	foreign.metadata[util.PolarisClusterName] = "other"
	sdk := sdkTestInstance{testInstance: testInstance{host: "10.0.0.4",
		metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "test"}}, port: 80}
	removedSDK := sdkTestInstance{testInstance: testInstance{host: "10.0.0.8",
		metadata: map[string]string{util.PolarisSource: "sdk", util.PolarisClusterName: "test"}}, port: 80}
	controllerHeartbeat := sdkTestInstance{testInstance: ownedInstance("10.0.0.6"), port: 80}
	currentController, currentAll := address.GetAddressMapFromPolarisInstance([]model.Instance{
		changed, legacy, sdk, foreign, controllerHeartbeat, ownedInstance("10.0.0.7"), removedSDK,
		sdkTestInstance{testInstance: testInstance{host: "10.0.0.1"}, port: 8080},
		sdkTestInstance{testInstance: testInstance{host: "2001:db8::1"}, port: 80},
	}, "test")
	add, del, update := p.CompareInstance(
		testService(), desiredHealthCheck{}, spec, currentController, currentAll)
	for _, tc := range []struct {
		name      string
		instances []address.Address
		want      map[string]bool
	}{
		{name: "add", instances: add, want: map[string]bool{"10.0.0.1-80": true}},
		{name: "delete", instances: del, want: map[string]bool{"10.0.0.7-80": true}},
		{name: "update", instances: update, want: map[string]bool{"10.0.0.2-80": true, "10.0.0.3-80": true, "10.0.0.6-80": true}},
	} {
		got := make(map[string]bool)
		for _, instance := range tc.instances {
			got[fmt.Sprintf("%s-%d", instance.IP, instance.Port)] = true
		}
		if len(tc.instances) != len(tc.want) || !maps.Equal(got, tc.want) {
			t.Errorf("%s instances=%v, want=%v", tc.name, got, tc.want)
		}
	}
	for _, instance := range update {
		if instance.PolarisInstance == nil || instance.PolarisInstance.GetHost() != instance.IP {
			t.Fatalf("update lost its original Polaris instance: %+v", instance)
		}
		if spec[fmt.Sprintf("%s-%d", instance.IP, instance.Port)].PolarisInstance != nil {
			t.Fatal("comparison mutated the endpoint address")
		}
	}
}
