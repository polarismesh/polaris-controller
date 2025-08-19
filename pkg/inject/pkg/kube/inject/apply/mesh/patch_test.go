package mesh

import (
	"testing"
)

func TestGetSidecarConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *SidecarConfig
		wantErr bool
	}{
		{
			name:  "valid full config",
			input: `{"dns-domain-suffix": "cluster.local", "dns-ttl": 30, "dns-recurse": true, "dns-recurse-timeout": 500}`,
			want: &SidecarConfig{
				DnsDomainSuffix:   stringPtr("cluster.local"),
				DnsTTL:            intPtr(30),
				DnsRecurseEnabled: boolPtr(true),
				DnsRecurseTimeout: intPtr(500),
			},
			wantErr: false,
		},
		{
			name:  "partial config",
			input: `{"dns-domain-suffix": "local", "dns-ttl": 10}`,
			want: &SidecarConfig{
				DnsDomainSuffix: stringPtr("local"),
				DnsTTL:          intPtr(10),
			},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid json",
			input:   `{"dns-domain-suffix": "local", "dns-ttl": }`,
			want:    nil,
			wantErr: true,
		},
		{
			name:  "empty json object",
			input: `{}`,
			want:  &SidecarConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getSidecarConfig(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("getSidecarConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !compareSidecarConfig(got, tt.want) {
				t.Errorf("getSidecarConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 辅助函数用于比较SidecarConfig
func compareSidecarConfig(a, b *SidecarConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalStringPtr(a.DnsDomainSuffix, b.DnsDomainSuffix) &&
		equalIntPtr(a.DnsTTL, b.DnsTTL) &&
		equalBoolPtr(a.DnsRecurseEnabled, b.DnsRecurseEnabled) &&
		equalIntPtr(a.DnsRecurseTimeout, b.DnsRecurseTimeout)
}

func equalStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func equalIntPtr(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// 辅助函数用于创建指针值
func stringPtr(s string) *string { return &s }
func intPtr(i int) *int          { return &i }
func boolPtr(b bool) *bool       { return &b }
