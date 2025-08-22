package mesh

import (
	"reflect"
	"testing"

	utils "github.com/polarismesh/polaris-controller/pkg/util"
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
			input: `{"dns":{"suffix":"cluster.local","ttl":30},"recurse":{"enabled":true,"timeout":500}}`,
			want: &SidecarConfig{
				Dns: &DnsConfig{
					Suffix: stringPtr("cluster.local"),
					TTL:    intPtr(30),
				},
				Recurse: &RecurseConfig{
					Enabled: boolPtr(true),
					Timeout: intPtr(500),
				},
			},
			wantErr: false,
		},
		{
			name:  "partial config",
			input: `{"dns":{"suffix":"local","ttl":10}}`,
			want: &SidecarConfig{
				Dns: &DnsConfig{
					Suffix: stringPtr("local"),
					TTL:    intPtr(10),
				},
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
			input:   `{"dns":{"suffix":"local","ttl":}}`,
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

func TestFillEnv(t *testing.T) {
	tests := []struct {
		name     string
		config   *SidecarConfig
		mode     utils.SidecarMode
		expected map[string]string
	}{
		{
			name: "TestLocationConfig",
			config: &SidecarConfig{
				Location: &Location{
					Region:     utils.StringPtr("ap-guangzhou"),
					Zone:       utils.StringPtr("zone-1"),
					Campus:     utils.StringPtr("campus-1"),
					MatchLevel: utils.StringPtr("region"),
				},
			},
			mode: utils.SidecarForDns,
			expected: map[string]string{
				EnvSidecarRegion:           "ap-guangzhou",
				EnvSidecarZone:             "zone-1",
				EnvSidecarCampus:           "campus-1",
				EnvSidecarNearbyMatchLevel: "region",
			},
		},
		{
			name: "TestInvalidLogLevel",
			config: &SidecarConfig{
				LogOptions: &LogOptions{
					OutputLevel: utils.StringPtr("invalid"),
				},
			},
			mode:     utils.SidecarForDns,
			expected: map[string]string{},
		},
		{
			name: "TestNonSidecarForDnsMode",
			config: &SidecarConfig{
				Dns: &DnsConfig{
					Suffix: utils.StringPtr("cluster.local"),
					TTL:    utils.IntPtr(30),
				},
			},
			mode:     utils.SidecarForMesh,
			expected: map[string]string{},
		},
		{
			name: "TestRecurseConfigDisabled",
			config: &SidecarConfig{
				Recurse: &RecurseConfig{
					Enabled: utils.BoolPtr(false),
					Timeout: utils.IntPtr(500),
				},
			},
			mode: utils.SidecarForDns,
			expected: map[string]string{
				EnvSidecarRecurseEnable:  "false",
				EnvSidecarRecurseTimeout: "500",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envMap := make(map[string]string)
			fillEnv(envMap, tt.config, tt.mode)
			if !reflect.DeepEqual(envMap, tt.expected) {
				t.Errorf("fillEnv() = %v, want %v", envMap, tt.expected)
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
	return compareDnsConfig(a.Dns, b.Dns) &&
		compareRecurseConfig(a.Recurse, b.Recurse) &&
		compareLocation(a.Location, b.Location) &&
		compareLogOptions(a.LogOptions, b.LogOptions)
}

func compareDnsConfig(a, b *DnsConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalStringPtr(a.Suffix, b.Suffix) &&
		equalIntPtr(a.TTL, b.TTL)
}

func compareRecurseConfig(a, b *RecurseConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalBoolPtr(a.Enabled, b.Enabled) &&
		equalIntPtr(a.Timeout, b.Timeout)
}

func compareLocation(a, b *Location) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalStringPtr(a.Region, b.Region) &&
		equalStringPtr(a.Zone, b.Zone) &&
		equalStringPtr(a.Campus, b.Campus)
}

func compareLogOptions(a, b *LogOptions) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return equalStringPtr(a.OutputLevel, b.OutputLevel) &&
		equalIntPtr(a.RotationMaxSize, b.RotationMaxSize) &&
		equalIntPtr(a.RotationMaxAge, b.RotationMaxAge) &&
		equalIntPtr(a.RotationMaxBackups, b.RotationMaxBackups)
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
