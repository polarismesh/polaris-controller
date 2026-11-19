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
