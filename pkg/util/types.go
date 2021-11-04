package util

const (
	PolarisWeight         = "polarismesh.cn/weight"
	PolarisHeartBeatTTL   = "polarismesh.cn/ttl"
	PolarisEnableRegister = "polarismesh.cn/enableRegister"
	WorkloadKind          = "polarismesh.cn/workloadKind"
	PolarisMetadata       = "polarismesh.cn/metadata"
	PolarisCustomWeight   = "polarismesh.cn/customWeight"
	PolarisAliasNamespace = "polarismesh.cn/aliasNamespace"
	PolarisAliasService   = "polarismesh.cn/aliasService"
)

const (
	PolarisClusterName = "clusterName"
	PolarisPlatform    = "platform"
	PolarisVersion     = "version"
	PolarisProtocol    = "protocol"
)

// PolarisSystemMetaSet 由 polaris controller 决定的 meta，用户如果在 custom meta 中设置了，不会生效
var PolarisSystemMetaSet = map[string]struct{}{PolarisClusterName: {}, PolarisPlatform: {}}

// PolarisDefaultMetaSet 由 polaris controller 托管的 service ，注册的实例必定会带的 meta，
// 用于判断用户的 custom meta 是否发生了更新
var PolarisDefaultMetaSet = map[string]struct{}{
	PolarisClusterName: {},
	PolarisPlatform:    {},
	PolarisVersion:     {},
	PolarisProtocol:    {},
}

// ServiceChangeType 发升变更的类型
type ServiceChangeType string

const (
	ServicePolarisDelete         ServiceChangeType = "servicePolarisDelete" // 删除了北极星的服务
	ServiceNameSpacesChanged     ServiceChangeType = "serviceNameSpacesChanged"
	ServiceNameChanged           ServiceChangeType = "serviceNameChanged"
	ServiceWeightChanged         ServiceChangeType = "serviceWeightChanged"
	ServiceTokenChanged          ServiceChangeType = "serviceTokenChanged"
	ServiceTTLChanged            ServiceChangeType = "serviceTTLChanged"
	ServiceEnableRegisterChanged ServiceChangeType = "serviceEnableRegisterChanged"
	ServiceMetadataChanged       ServiceChangeType = "serviceMetadataChanged"
	ServiceCustomWeightChanged   ServiceChangeType = "serviceCustomWeightChanged"
)

// IndexPortMap 对应{"index-port":weight}
type IndexPortMap map[string]int
