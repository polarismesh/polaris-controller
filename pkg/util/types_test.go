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

package util

import "testing"

func TestParseSidecarMode(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want SidecarMode
	}{
		{name: "mesh", val: "mesh", want: SidecarForMesh},
		{name: "dns", val: "dns", want: SidecarForDns},
		{name: "java-agent", val: "java-agent", want: SidecarForJavaAgent},
		{name: "empty", val: "", want: SidecarForUnknown},
		{name: "none", val: SidecarNoneModeName, want: SidecarForUnknown},
		{name: "invalid", val: "dnsagent", want: SidecarForUnknown},
		{name: "case-sensitive", val: "Mesh", want: SidecarForUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseSidecarMode(tc.val); got != tc.want {
				t.Fatalf("ParseSidecarMode(%q) = %d, want %d", tc.val, got, tc.want)
			}
		})
	}
}

func TestParseSidecarModeName(t *testing.T) {
	tests := []struct {
		name string
		mode SidecarMode
		want string
	}{
		{name: "mesh", mode: SidecarForMesh, want: SidecarMeshModeName},
		{name: "dns", mode: SidecarForDns, want: SidecarDnsModeName},
		{name: "java-agent", mode: SidecarForJavaAgent, want: SidecarJavaAgentModeName},
		{name: "unknown", mode: SidecarForUnknown, want: SidecarUnknownModeName},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseSidecarModeName(tc.mode); got != tc.want {
				t.Fatalf("ParseSidecarModeName(%d) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}
