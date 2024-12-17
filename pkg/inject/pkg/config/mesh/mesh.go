// Copyright 2017 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mesh

import (
	"os"

	"github.com/hashicorp/go-multierror"
	"gopkg.in/yaml.v2"
)

type MeshConfig struct {
	DefaultConfig *DefaultConfig `yaml:"defaultConfig"`
}

func (m *MeshConfig) Clone() *MeshConfig {
	copyData := &MeshConfig{
		DefaultConfig: &DefaultConfig{
			ProxyMetadata: map[string]string{},
		},
	}

	for k, v := range m.DefaultConfig.ProxyMetadata {
		copyData.DefaultConfig.ProxyMetadata[k] = v
	}
	return copyData
}

type DefaultConfig struct {
	ProxyMetadata map[string]string `yaml:"proxyMetadata"`
}

// DefaultMeshConfig configuration
func DefaultMeshConfig() MeshConfig {
	return MeshConfig{
		DefaultConfig: &DefaultConfig{
			ProxyMetadata: map[string]string{},
		},
	}
}

// ApplyMeshConfig returns a new MeshConfig decoded from the
// input YAML with the provided defaults applied to omitted configuration values.
func ApplyMeshConfig(str string, defaultConfig MeshConfig) (*MeshConfig, error) {
	if err := yaml.Unmarshal([]byte(str), &defaultConfig); err != nil {
		return nil, err
	}
	return &defaultConfig, nil
}

// ApplyMeshConfigDefaults returns a new MeshConfig decoded from the
// input YAML with defaults applied to omitted configuration values.
func ApplyMeshConfigDefaults(yaml string) (*MeshConfig, error) {
	return ApplyMeshConfig(yaml, DefaultMeshConfig())
}

// ReadMeshConfig gets mesh configuration from a config file
func ReadMeshConfig(filename string) (*MeshConfig, error) {
	yaml, err := os.ReadFile(filename)
	if err != nil {
		return nil, multierror.Prefix(err, "cannot read mesh config file")
	}
	return ApplyMeshConfigDefaults(string(yaml))
}
