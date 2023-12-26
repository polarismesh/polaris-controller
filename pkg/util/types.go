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

const (
	PolarisSync           = "polarismesh.cn/sync"
	PolarisEnableRegister = "polarismesh.cn/enableRegister"
	PolarisAliasNamespace = "polarismesh.cn/aliasNamespace"
	PolarisAliasService   = "polarismesh.cn/aliasService"
	PolarisSidecarMode    = "polarismesh.cn/sidecar-mode"
	PolarisMetadata       = "polarismesh.cn/metadata"
	PolarisWeight         = "polarismesh.cn/weight"
	PolarisHeartBeatTTL   = "polarismesh.cn/ttl"

	WorkloadKind        = "polarismesh.cn/workloadKind"
	PolarisCustomWeight = "polarismesh.cn/customWeight"

	PolarisCustomVersion = "polarismesh.cn/customVersion"

	PolarisConfigGroup       = "polarismesh.cn/configGroup"
	PolarisConfigEncrypt     = "polarismesh.cn/enableEncrypt"
	PolarisConfigEncryptAlog = "polarismesh.cn/enableEncryptAlog"

	PolarisTLSMode = "polarismesh.cn/tls-mode"
	// SidecarServiceName xds metadata key when node is run in sidecar mode
	SidecarServiceName = "sidecar.polarismesh.cn/serviceName"
	// SidecarNamespaceName xds metadata key when node is run in sidecar mode
	SidecarNamespaceName = "sidecar.polarismesh.cn/serviceNamespace"
	// SidecarBindPort xds metadata key when node is run in sidecar mode
	SidecarBindPort = "sidecar.polarismesh.cn/bindPorts"
	// SidecarEnvoyMetadata
	SidecarEnvoyMetadata = "sidecar.polarismesh.cn/envoyMetadata"

	PolarisSidecarModeLabel = "polaris-sidecar-mode"
)

const (
	PolarisClusterName = "clusterName"
	PolarisSource      = "source"
	PolarisVersion     = "version"
	PolarisProtocol    = "protocol"

	// PolarisOldSource 旧版本 controller 用来标志是 controller 同步的服务实例。
	// 已经废弃，项目中当前用来兼容存量的实例。
	PolarisOldSource = "platform"
)

const (
	SyncModeAll    = "all"
	SyncModeDemand = "demand"
	IsEnableSync   = "true"
	IsDisableSync  = "false"
)

const (
	SyncDirectionKubernetesToPolaris = "kubernetesToPolaris"
	SyncDirectionPolarisToKubernetes = "polarisToKubernetes"
	SyncDirectionBoth                = "both"
)

const (
	SourceFromKubernetes = "kubernetes"
	SourceFromPolaris    = "polaris"
)

const (
	ConflictModeIgnore  = "ignore"
	ConflictModeReplace = "replace"
)

const (
	MTLSModeNone       = "none"
	MTLSModeStrict     = "strict"
	MTLSModePermissive = "permissive"
)

const (
	InternalConfigFileSyncSourceKey        = "internal-sync-source"
	InternalConfigFileSyncSourceClusterKey = "internal-sync-sourcecluster"
)

// PolarisSystemMetaSet 由 polaris controller 决定的 meta，用户如果在 custom meta 中设置了，不会生效
var PolarisSystemMetaSet = map[string]struct{}{PolarisClusterName: {}, PolarisSource: {}}

// PolarisDefaultMetaSet 由 polaris controller 托管的 service ，注册的实例必定会带的 meta，
// 用于判断用户的 custom meta 是否发生了更新
var PolarisDefaultMetaSet = map[string]struct{}{
	PolarisClusterName: {},
	PolarisSource:      {},
	PolarisVersion:     {},
	PolarisProtocol:    {},
}

// ServiceChangeType 发升变更的类型
type ServiceChangeType string

const (
	ServicePolarisDelete          ServiceChangeType = "servicePolarisDelete" // 删除了北极星的服务
	ServiceNameSpacesChanged      ServiceChangeType = "serviceNameSpacesChanged"
	ServiceNameChanged            ServiceChangeType = "serviceNameChanged"
	ServiceTokenChanged           ServiceChangeType = "serviceTokenChanged"
	ServiceMetadataChanged        ServiceChangeType = "ServiceMetadataChanged"
	InstanceTTLChanged            ServiceChangeType = "InstanceTTLChanged"
	InstanceWeightChanged         ServiceChangeType = "InstanceWeightChanged"
	InstanceEnableRegisterChanged ServiceChangeType = "InstanceEnableRegisterChanged"
	InstanceMetadataChanged       ServiceChangeType = "InstanceMetadataChanged"
	InstanceCustomWeightChanged   ServiceChangeType = "InstanceCustomWeightChanged"
)

const (
	PolarisGoConfigFileTpl string = "polaris-client-config-tpl"
	PolarisGoConfigFile    string = "polaris-client-config"
)

const (
	PolarisSidecarRootCert string = "polaris-sidecar-secret"
)

type SidecarMode int

const (
	SidecarForUnknown SidecarMode = iota
	SidecarForMesh
	SidecarForDns

	SidecarMeshModeName string = "mesh"
	SidecarDnsModeName  string = "dns"
)

func ParseSidecarMode(val string) SidecarMode {
	if val == SidecarMeshModeName {
		return SidecarForMesh
	}
	if val == SidecarDnsModeName {
		return SidecarForDns
	}
	return SidecarForMesh
}

func ParseSidecarModeName(mode SidecarMode) string {
	if mode == SidecarForMesh {
		return SidecarMeshModeName
	}
	if mode == SidecarForDns {
		return SidecarDnsModeName
	}
	return SidecarMeshModeName
}

// IndexPortMap 对应{"index-port":weight}
type IndexPortMap map[string]int
