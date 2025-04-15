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

package app

import (
	"os"

	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	"github.com/polarismesh/polaris-controller/common"
	"github.com/polarismesh/polaris-controller/common/log"
)

// ProxyMetadata mesh envoy用到的配置
type ProxyMetadata struct {
	ServerAddress string `yaml:"serverAddress"`
	ClusterName   string `yaml:"clusterName"`
	OpenDemand    string `yaml:"openDemand"`
	CAAddress     string `yaml:"caAddress"`
}

// DefaultConfig mesh envoy sidecar 用到的配置
type DefaultConfig struct {
	ProxyMetadata ProxyMetadata `yaml:"proxyMetadata"`
}

// SidecarInject sidecar 注入相关
type SidecarInject struct {
	Mode    string      `yaml:"mode"`
	Ignores []IgnorePod `yaml:"ignorePods"`
}

type IgnorePod struct {
	Namespace string `yaml:"namespace"`
	PodName   string `yaml:"podName"`
}

type Server struct {
	// 健康探测时间间隔
	HealthCheckDuration string `yaml:"healthCheckDuration"`
	// 定时对账时间间隔
	ResyncDuration string `yaml:"resyncDuration"`
}

type controllerConfig struct {
	// 北极星服务端地址
	ServerAddress string `yaml:"serverAddress"`
	// 北极星服务端token(北极星开启鉴权时需要配置)
	PolarisAccessToken string `yaml:"accessToken"`
	// Operator 北极星主账户ID, 用于数据同步
	Operator string `yaml:"operator"`
	// 容器集群名称或ID
	ClusterName string `yaml:"clusterName"`
	// k8s服务同步配置
	ServiceSync *options.ServiceSync `yaml:"serviceSync"`
	// 配置同步配置
	ConfigSync *options.ConfigSync `yaml:"configSync"`
	// sidecar注入相关配置
	SidecarInject SidecarInject `yaml:"sidecarInject"`
	// mesh envoy 相关配置
	DefaultConfig DefaultConfig `yaml:"defaultConfig"`
	// 组件日志配置
	Logger map[string]*log.Options `yaml:"logger"`
	// 健康检查和对账配置
	Server Server `yaml:"server"`
}

func (c *controllerConfig) getPolarisServerAddress() string {
	// 新配置格式
	if c.ServerAddress != "" {
		return c.ServerAddress
	}
	// 老的配置格式
	if c.ServiceSync.ServerAddress != "" {
		return c.ServiceSync.ServerAddress
	}
	return common.PolarisServerAddress
}

func (c *controllerConfig) getPolarisAccessToken() string {
	// 新配置格式
	if c.PolarisAccessToken != "" {
		return c.PolarisAccessToken
	}
	// 老的配置格式
	if c.ServiceSync.PolarisAccessToken != "" {
		return c.ServiceSync.PolarisAccessToken
	}
	return ""
}

func (c *controllerConfig) getPolarisOperator() string {
	// 新配置格式
	if c.Operator != "" {
		return c.Operator
	}
	// 老的配置格式
	if c.ServiceSync.Operator != "" {
		return c.ServiceSync.Operator
	}
	return ""
}

func readConfFromFile() (*controllerConfig, error) {
	buf, err := os.ReadFile(BootstrapConfigFile)
	if err != nil {
		log.Errorf("read file error, %v", err)
		return nil, err
	}

	c := &controllerConfig{}
	err = yaml.Unmarshal(buf, c)
	if err != nil {
		log.Errorf("unmarshal config error, %v", err)
		return nil, err
	}

	return c, nil
}
