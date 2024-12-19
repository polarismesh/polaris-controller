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
	"github.com/polarismesh/polaris-controller/common/log"
)

// ServiceSync controller 用到的配置
type ProxyMetadata struct {
	ServerAddress string `yaml:"serverAddress"`
	ClusterName   string `yaml:"clusterName"`
	OpenDemand    string `yaml:"openDemand"`
	CAAddress     string `yaml:"caAddress"`
}

// DefaultConfig controller 用到的配置
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
	Logger        map[string]*log.Options `yaml:"logger"`
	ClusterName   string                  `yaml:"clusterName"`
	Server        Server                  `yaml:"server"`
	ServiceSync   *options.ServiceSync    `yaml:"serviceSync"`
	ConfigSync    *options.ConfigSync     `yaml:"configSync"`
	SidecarInject SidecarInject           `yaml:"sidecarInject"`
}

func readConfFromFile() (*controllerConfig, error) {
	buf, err := os.ReadFile(MeshFile)
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
