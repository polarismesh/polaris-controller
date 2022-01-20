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
	PolarisSync           = "polarismesh.cn/sync"

	// PolarisServiceSyncAnno 当同步模式是 demand 时，会给 ns 下的 service 上打下面这个 anno
	// 这个 anno 只在 polaris-controller 内部用，用户无需关心。
	PolarisServiceSyncAnno = "polarismesh.cn/namespaceSync"
)

const (
	PolarisClusterName = "clusterName"
	PolarisPlatform    = "platform"
	PolarisVersion     = "version"
	PolarisProtocol    = "protocol"
)

const (
	SyncModeAll = "all"
	SyncModeDemand = "demand"
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
