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

package options

import (
	"time"

	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolarisControllerOptions
type PolarisControllerOptions struct {
	*PolarisControllerConfiguration
}

// ServiceSync 服务同步相关配置
type ServiceSync struct {
	Mode          string `yaml:"mode"`
	ServerAddress string `yaml:"serverAddress"`
	// 以下配置仅 polaris-server 开启 console auth
	// 调用 polaris-server OpenAPI 的凭据
	PolarisAccessToken string `yaml:"accessToken"`
	// Operator 用于数据同步的帐户ID
	Operator string `yaml:"operator"`
	// Enable 开启同步
	Enable bool `yaml:"enable"`
}

// ConfigSync 服务同步相关配置
type ConfigSync struct {
	Mode          string `yaml:"mode"`
	ServerAddress string `yaml:"serverAddress"`
	// 以下配置仅 polaris-server 开启 console auth
	// 调用 polaris-server OpenAPI 的凭据
	PolarisAccessToken string `yaml:"accessToken"`
	// Operator 用于数据同步的帐户ID
	Operator string `yaml:"operator"`
	// AllowDelete 允许向 Polaris 发起删除操作
	AllowDelete bool `yaml:"allowDelete"`
	// SyncDirection 配置同步方向, kubernetesToPolaris/polarisToKubernetes/both
	// kubernetesToPolaris: 配置数据只能从 kubernetes 同步到 polaris
	// polarisToKubernetes: 配置数据只能从 polaris 同步到 kubernetes
	// both: 配置数据能从 kubernetes 同步到 polaris, 也能从 polaris 同步到 kubernetes, 但是不会出现循环同步
	SyncDirection string `yaml:"syncDirection"`
	// ConflictMode 同步冲突策略
	ConflictMode string `yaml:"conflictMode"`
	// Enable 开启同步
	Enable bool `yaml:"enable"`
	// IgnoreNamespaces 忽略同步的命名空间，默认不忽略
	IgnoreNamespaces []string `yaml:"ignoreNamespaces"`
	// DefaultGroup 配置分组同步默认分组名称
	DefaultGroup string `yaml:"defaultGroup"`
}

// PolarisControllerConfiguration holds configuration for a polaris controller
type PolarisControllerConfiguration struct {
	// port is the port that the controller-manager's http service runs on.
	ClusterName string
	// ConcurrentPolarisSyncs 同步任务处理工作协程数量
	ConcurrentPolarisSyncs int
	// Size 数据同步批中的元素数量，最大只能为 100
	Size int
	// MinAccountingPeriod
	MinAccountingPeriod metav1.Duration
	// SyncMode 同步类型，按需(demand)/全量(all)
	SyncMode string
	// SidecarMode sidecar 注入模型 mesh/dns
	SidecarMode string
	// HealthCheckDuration 检查 polaris-server 集群健康状态周期
	HealthCheckDuration time.Duration
	// ResyncDuration 对账任务执行时间
	ResyncDuration time.Duration
	// ConfigSync 配置中心同步配置
	ConfigSync *ConfigSync
}

// AddFlags adds flags related to generic for controller manager to the specified FlagSet.
func (o *PolarisControllerOptions) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}
	fs.StringVar(&o.ClusterName, "cluster-name", "", "clusterName")
	fs.IntVar(&o.ConcurrentPolarisSyncs, "concurrent-polaris-syncs", 5, "service queue workers")
	fs.IntVar(&o.Size, "concurrency-polaris-size", 100, "polaris request size pre time")
	fs.DurationVar(
		&o.MinAccountingPeriod.Duration, "min-accounting-period",
		o.MinAccountingPeriod.Duration,
		"The resync period in reflectors will be random between MinResyncPeriod and 2*MinResyncPeriod.")
	fs.StringVar(&o.SyncMode, "sync-mode", "", "polaris-controller sync mode, supports 'all', 'demand'")
	fs.StringVar(&o.SidecarMode, "sidecarinject-mode", "", "polaris-controller sidecarinject mode, supports 'mesh', 'dns'")
	fs.DurationVar(&o.HealthCheckDuration, "healthcheck-duration", time.Second,
		"The health checking duration of the polaris server (eg. 5h30m2s).")
	fs.DurationVar(&o.ResyncDuration, "resync-duration", time.Second*30, "The resync duration (eg. 5h30m2s).")
}

// ApplyTo fills up generic config with options.
func (o *PolarisControllerOptions) ApplyTo(cfg *PolarisControllerConfiguration) error {
	if o == nil {
		return nil
	}

	cfg.ClusterName = o.ClusterName
	cfg.ConcurrentPolarisSyncs = o.ConcurrentPolarisSyncs
	cfg.Size = o.Size
	if cfg.Size > 100 {
		cfg.Size = 100
	}
	cfg.MinAccountingPeriod = o.MinAccountingPeriod
	cfg.SyncMode = o.SyncMode
	cfg.SidecarMode = o.SidecarMode
	cfg.HealthCheckDuration = o.HealthCheckDuration
	cfg.ResyncDuration = o.ResyncDuration
	cfg.ConfigSync = o.ConfigSync
	return nil
}
