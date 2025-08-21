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
		name   string
		config *SidecarConfig
		mode   utils.SidecarMode
		want   map[string]string
	}{
		{
			name: "full log options",
			config: &SidecarConfig{
				LogOptions: &LogOptions{
					OutputLevel:        stringPtr("info"),
					RotationMaxSize:    intPtr(100),
					RotationMaxAge:     intPtr(7),
					RotationMaxBackups: intPtr(5),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{
				EnvSidecarLogLevel:              "info",
				EnvSidecarLogRotationMaxSize:    "100",
				EnvSidecarLogRotationMaxAge:     "7",
				EnvSidecarLogRotationMaxBackups: "5",
			},
		},
		{
			name: "dns config with suffix and ttl",
			config: &SidecarConfig{
				Dns: &DnsConfig{
					Suffix: stringPtr("cluster.local"),
					TTL:    intPtr(30),
				},
			},
			mode: utils.SidecarForDns,
			want: map[string]string{
				EnvSidecarDnsSuffix: "cluster.local",
				EnvSidecarDnsTtl:    "30",
			},
		},
		{
			name: "recurse config with enabled and timeout",
			config: &SidecarConfig{
				Recurse: &RecurseConfig{
					Timeout: intPtr(500),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{
				EnvSidecarRecurseTimeout: "500",
			},
		},
		{
			name:   "empty config",
			config: &SidecarConfig{},
			mode:   utils.SidecarForMesh,
			want:   map[string]string{},
		},
		{
			name: "invalid log level",
			config: &SidecarConfig{
				LogOptions: &LogOptions{
					OutputLevel: stringPtr("invalid"),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{},
		},
		{
			name: "location config with region, zone, and campus",
			config: &SidecarConfig{
				Location: &Location{
					Region: stringPtr("ap-guangzhou"),
					Zone:   stringPtr("zone-1"),
					Campus: stringPtr("campus-a"),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{
				EnvSidecarRegion: "ap-guangzhou",
				EnvSidecarZone:   "zone-1",
				EnvSidecarCampus: "campus-a",
			},
		},
		{
			name: "dns config in non-dns mode",
			config: &SidecarConfig{
				Dns: &DnsConfig{
					Suffix: stringPtr("cluster.local"),
					TTL:    intPtr(30),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{},
		},
		{
			name: "recurse config with enabled false",
			config: &SidecarConfig{
				Recurse: &RecurseConfig{
					Enabled: boolPtr(false),
					Timeout: intPtr(500),
				},
			},
			mode: utils.SidecarForMesh,
			want: map[string]string{
				EnvSidecarRecurseEnable:  "false",
				EnvSidecarRecurseTimeout: "500",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envMap := make(map[string]string)
			fillEnv(envMap, tt.config, tt.mode)
			if !reflect.DeepEqual(envMap, tt.want) {
				t.Errorf("fillEnv() = %v, want %v", envMap, tt.want)
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
