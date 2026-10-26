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
	"fmt"
	"testing"

	v1 "k8s.io/api/core/v1"
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
			result := buildAddresses(endpoint, subset, true, corelisters.NewPodLister(pods), 37, util.IndexPortMap{"0-80": tc.weight}, false)
			if got := result["10.0.0.1-80"].Weight; got != tc.want {
				t.Fatalf("weight=%d want=%d", got, tc.want)
			}
		})
	}
}
