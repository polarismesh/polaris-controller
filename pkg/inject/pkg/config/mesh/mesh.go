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
	"fmt"
	"os"

	"github.com/hashicorp/go-multierror"
	"gopkg.in/yaml.v2"
)

// MeshEnvoyConfig mesh 注入配置, envoy sidecar当前有用到
type MeshEnvoyConfig struct {
	DefaultConfig *DefaultConfig `yaml:"DefaultConfig"`
}

// DefaultConfig 存储北极星proxy默认配置和用户自定义配置
type DefaultConfig struct {
	ProxyMetadata map[string]string `yaml:"ProxyMetadata"`
}

// ReadMeshEnvoyConfig 读取mesh envoy sidecar注入配置
func ReadMeshEnvoyConfig(filename string) (*MeshEnvoyConfig, error) {
	yamlBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, multierror.Prefix(err, "cannot read mesh config file")
	}
	defaultConfig := &MeshEnvoyConfig{
		DefaultConfig: &DefaultConfig{
			ProxyMetadata: map[string]string{},
		},
	}
	if err = yaml.Unmarshal(yamlBytes, defaultConfig); err != nil {
		return nil, err
	}
	return defaultConfig, nil
}

// GetDefaultConfig 获取默认配置
func (ic *MeshEnvoyConfig) GetDefaultConfig() *DefaultConfig {
	if ic == nil {
		return nil
	}
	return ic.DefaultConfig
}

// SetDefaultConfig 设置默认配置
func (ic *MeshEnvoyConfig) SetDefaultConfig(config *DefaultConfig) {
	if ic == nil {
		return
	}
	ic.DefaultConfig = config
}

// GetProxyMetadata 获取 proxy 元数据
func (dc *DefaultConfig) GetProxyMetadata() map[string]string {
	if dc == nil {
		return nil
	}
	return dc.ProxyMetadata
}

// SetProxyMetadataWithKV 设置 proxy 元数据
func (dc *MeshEnvoyConfig) SetProxyMetadataWithKV(k, v string) {
	if dc == nil {
		return
	}
	dc.DefaultConfig.ProxyMetadata[k] = v
}

// String 返回 MeshEnvoyConfig 的字符串表示
func (ic *MeshEnvoyConfig) String() string {
	if ic == nil {
		return "MeshEnvoyConfig{nil}"
	}

	var defaultConfig string
	if ic.DefaultConfig == nil {
		defaultConfig = "nil"
	} else {
		defaultConfig = ic.DefaultConfig.String()
	}

	return fmt.Sprintf("MeshEnvoyConfig{DefaultConfig: %s}", defaultConfig)
}

// String 返回 DefaultConfig 的字符串表示
func (dc *DefaultConfig) String() string {
	if dc == nil {
		return "DefaultConfig{nil}"
	}

	metadata := "nil"
	if dc.ProxyMetadata != nil {
		metadata = fmt.Sprintf("%v", dc.ProxyMetadata)
	}

	return fmt.Sprintf("DefaultConfig{ProxyMetadata: %s}", metadata)
}
