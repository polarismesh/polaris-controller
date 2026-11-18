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

package address

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestCustomWeightBounds(t *testing.T) {
	pods := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	if err := pods.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "app-0"}}); err != nil {
		t.Fatal(err)
	}
	endpoint := &v1.EndpointAddress{IP: "10.0.0.1", TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "app-0"}}
	subset := &v1.EndpointSubset{Ports: []v1.EndpointPort{{Port: 80}}}
	for _, tc := range []struct{ weight, want int }{{-1, 37}, {0, 0}, {1, 1}, {100, 100}, {101, 37}} {
		t.Run(fmt.Sprint(tc.weight), func(t *testing.T) {
			result, err := buildAddresses(endpoint, subset, true, corelisters.NewPodLister(pods),
				37, util.IndexPortMap{"0-80": tc.weight}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := result["10.0.0.1-80"].Weight; got != tc.want {
				t.Fatalf("weight=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestBuildAddressesUsesEndpointPortProtocol(t *testing.T) {
	pods := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	if err := pods.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "app-0"}}); err != nil {
		t.Fatal(err)
	}
	endpoint := &v1.EndpointAddress{IP: "10.0.0.1",
		TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "app-0"}}
	longPortName := strings.Repeat("p", 64)
	subset := &v1.EndpointSubset{Ports: []v1.EndpointPort{
		{Name: longPortName, Port: 80, Protocol: v1.ProtocolUDP},
		{Name: longPortName, Port: 81, Protocol: v1.ProtocolSCTP},
		{Name: longPortName, Port: 82},
	}}

	result, err := buildAddresses(endpoint, subset, false, corelisters.NewPodLister(pods), 100, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for port, want := range map[int]string{80: "UDP", 81: "SCTP", 82: "TCP"} {
		key := fmt.Sprintf("10.0.0.1-%d", port)
		if got := result[key].Protocol; got != want {
			t.Fatalf("port %d protocol=%q, want %q", port, got, want)
		}
		if len(result[key].Protocol) > 32 {
			t.Fatalf("port %d protocol exceeds 32 characters: %q", port, result[key].Protocol)
		}
	}
}

func TestParseCustomProtocols(t *testing.T) {
	service := &v1.Service{Spec: v1.ServiceSpec{Ports: []v1.ServicePort{
		{Name: "http", Port: 80},
		{Name: "grpc", Port: 9090},
		{Port: 8081},
	}}}

	for _, tc := range []struct {
		name       string
		annotation string
		want       map[string]string
		wantErr    string
	}{
		{name: "empty", annotation: "", want: nil},
		{name: "valid", annotation: `{"http":"http","grpc":"grpc"}`,
			want: map[string]string{"http": "http", "grpc": "grpc"}},
		{name: "empty object", annotation: `{}`, want: map[string]string{}},
		{name: "invalid JSON", annotation: `{"http":`, wantErr: "JSON object"},
		{name: "null", annotation: `null`, wantErr: "JSON object"},
		{name: "non-string value", annotation: `{"http":80}`, wantErr: "JSON object"},
		{name: "empty port name", annotation: `{"":"grpc"}`, wantErr: "empty port name"},
		{name: "unknown port name", annotation: `{"httpp":"http"}`, wantErr: "unknown Service port name"},
		{name: "unnamed port key", annotation: `{"":"grpc"}`, wantErr: "empty port name"},
		{name: "empty protocol", annotation: `{"http":""}`, wantErr: "invalid protocol"},
		{name: "blank protocol", annotation: `{"http":" grpc "}`, wantErr: "invalid protocol"},
		{name: "protocol too long", annotation: `{"http":"` + strings.Repeat("p", 33) + `"}`,
			wantErr: "exceeds 32 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service.Annotations = map[string]string{util.PolarisProtocols: tc.annotation}
			got, err := parseCustomProtocols(service)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error=%v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("protocols=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestEndpointAddressesApplyCustomProtocols(t *testing.T) {
	pods := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	if err := pods.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod"}}); err != nil {
		t.Fatal(err)
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.PolarisProtocols: `{"http":"http","grpc":"grpc"}`,
		}},
		Spec: v1.ServiceSpec{Ports: []v1.ServicePort{
			{Name: "http", Port: 80, Protocol: v1.ProtocolTCP},
			{Name: "grpc", Port: 9090, Protocol: v1.ProtocolTCP},
			{Port: 8081, Protocol: v1.ProtocolUDP},
		}},
	}
	endpoint := &v1.Endpoints{Subsets: []v1.EndpointSubset{{
		Addresses: []v1.EndpointAddress{{IP: "10.0.0.1",
			TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "pod"}}},
		Ports: []v1.EndpointPort{
			{Name: "http", Port: 80, Protocol: v1.ProtocolTCP},
			{Name: "grpc", Port: 9090, Protocol: v1.ProtocolTCP},
			{Port: 8081, Protocol: v1.ProtocolUDP},
			{Name: "admin", Port: 8082, Protocol: v1.ProtocolSCTP},
		},
	}}}

	addresses, err := GetAddressMapFromEndpoints(service, endpoint, corelisters.NewPodLister(pods), nil)
	if err != nil {
		t.Fatal(err)
	}
	for port, want := range map[int]string{80: "http", 9090: "grpc", 8081: "UDP", 8082: "SCTP"} {
		key := fmt.Sprintf("10.0.0.1-%d", port)
		if got := addresses[key].Protocol; got != want {
			t.Fatalf("port %d protocol=%q, want %q", port, got, want)
		}
	}
}

type failingPodLister struct {
	corelisters.PodLister
	err error
}

func (l failingPodLister) Pods(string) corelisters.PodNamespaceLister {
	return failingPodNamespaceLister{err: l.err}
}

type failingPodNamespaceLister struct {
	corelisters.PodNamespaceLister
	err error
}

func (l failingPodNamespaceLister) Get(string) (*v1.Pod, error) {
	return nil, l.err
}

func TestEndpointAddressesRejectIncompletePodData(t *testing.T) {
	lookupErr := errors.New("Pod cache unavailable")
	for _, tc := range []struct {
		name      string
		targetRef *v1.ObjectReference
		lookupErr error
	}{
		{name: "missing Pod", targetRef: &v1.ObjectReference{Namespace: "ns", Name: "missing"}},
		{name: "lookup error", targetRef: &v1.ObjectReference{Namespace: "ns", Name: "missing"}, lookupErr: lookupErr},
		{name: "missing target reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pods := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			if err := pods.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pod"}}); err != nil {
				t.Fatal(err)
			}
			podLister := corelisters.NewPodLister(pods)
			if tc.lookupErr != nil {
				podLister = failingPodLister{err: tc.lookupErr}
			}
			svc := &v1.Service{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{util.WorkloadKind: "StatefulSet"}}}
			ep := &v1.Endpoints{Subsets: []v1.EndpointSubset{{
				Addresses: []v1.EndpointAddress{
					{IP: "10.0.0.1", TargetRef: &v1.ObjectReference{Namespace: "ns", Name: "pod"}},
					{IP: "10.0.0.2", TargetRef: tc.targetRef},
				},
				Ports: []v1.EndpointPort{{Port: 80}},
			}}}
			addresses, err := GetAddressMapFromEndpoints(svc, ep, podLister, nil)
			if err == nil || addresses != nil {
				t.Fatalf("incomplete Pod data returned addresses=%v err=%v", addresses, err)
			}
			if tc.lookupErr != nil {
				if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), "ns/pod") {
					t.Fatalf("lookup error lost its cause or Pod context: %v", err)
				}
			} else if !strings.Contains(err.Error(), "10.0.0.2") {
				t.Fatalf("missing endpoint context: %v", err)
			} else if tc.targetRef != nil && !k8serrors.IsNotFound(err) {
				t.Fatalf("Pod not-found error lost its cause: %v", err)
			}
		})
	}
}
