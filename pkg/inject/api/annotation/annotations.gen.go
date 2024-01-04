
// GENERATED FILE -- DO NOT EDIT

package annotation

type ResourceTypes int

const (
	Unknown ResourceTypes = iota
    Any
    Ingress
    Pod
    Service
    ServiceEntry
)

func (r ResourceTypes) String() string {
	switch r {
	case 1:
		return "Any"
	case 2:
		return "Ingress"
	case 3:
		return "Pod"
	case 4:
		return "Service"
	case 5:
		return "ServiceEntry"
	}
	return "Unknown"
}

// Instance describes a single resource annotation
type Instance struct {
	// The name of the annotation.
	Name string

	// Description of the annotation.
	Description string

	// Hide the existence of this annotation when outputting usage information.
	Hidden bool

	// Mark this annotation as deprecated when generating usage information.
	Deprecated bool

	// The types of resources this annotation applies to.
	Resources []ResourceTypes
}

var (
		SidecarStatusReadinessApplicationPorts = Instance {
          Name: "readiness.status.sidecar.polarismesh.cn/applicationPorts",
          Description: "Specifies the list of ports exposed by the application "+
                        "container. Used by the Envoy sidecar readiness probe to "+
                        "determine that Envoy is configured and ready to receive "+
                        "traffic.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatusReadinessFailureThreshold = Instance {
          Name: "readiness.status.sidecar.polarismesh.cn/failureThreshold",
          Description: "Specifies the failure threshold for the Envoy sidecar "+
                        "readiness probe.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatusReadinessInitialDelaySeconds = Instance {
          Name: "readiness.status.sidecar.polarismesh.cn/initialDelaySeconds",
          Description: "Specifies the initial delay (in seconds) for the Envoy "+
                        "sidecar readiness probe.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatusReadinessPeriodSeconds = Instance {
          Name: "readiness.status.sidecar.polarismesh.cn/periodSeconds",
          Description: "Specifies the period (in seconds) for the Envoy sidecar "+
                        "readiness probe.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SecurityAutoMTLS = Instance {
          Name: "security.polarismesh.cn/autoMTLS",
          Description: "Determines whether the client proxy uses auto mTLS. This "+
                        "overrides the mesh default specified in "+
                        "MeshConfig.enable_auto_mtls.",
          Hidden: true,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarBootstrapOverride = Instance {
          Name: "sidecar.polarismesh.cn/bootstrapOverride",
          Description: "Specifies an alternative Envoy bootstrap configuration "+
                        "file.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarDiscoveryAddress = Instance {
          Name: "sidecar.polarismesh.cn/discoveryAddress",
          Description: "Specifies the XDS discovery address to be used by the "+
                        "Envoy sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarEnableCoreDump = Instance {
          Name: "sidecar.polarismesh.cn/enableCoreDump",
          Description: "Specifies whether or not an Envoy sidecar should enable "+
                        "core dump.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarInject = Instance {
          Name: "sidecar.polarismesh.cn/inject",
          Description: "Specifies whether or not an Polaris sidecar should be "+
                        "automatically injected into the workload.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarInterceptionMode = Instance {
          Name: "sidecar.polarismesh.cn/interceptionMode",
          Description: "Specifies the mode used to redirect inbound connections "+
                        "to Envoy (REDIRECT or TPROXY).",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarLogLevel = Instance {
          Name: "sidecar.polarismesh.cn/logLevel",
          Description: "Specifies the log level for Envoy.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarProxyCPU = Instance {
          Name: "sidecar.polarismesh.cn/proxyCPU",
          Description: "Specifies the requested CPU setting for the Envoy "+
                        "sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarProxyImage = Instance {
          Name: "sidecar.polarismesh.cn/proxyImage",
          Description: "Specifies the Docker image to be used by the Envoy "+
                        "sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarProxyMemory = Instance {
          Name: "sidecar.polarismesh.cn/proxyMemory",
          Description: "Specifies the requested memory setting for the Envoy "+
                        "sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarRewriteAppHTTPProbers = Instance {
          Name: "sidecar.polarismesh.cn/rewriteAppHTTPProbers",
          Description: "Rewrite HTTP readiness and liveness probes to be "+
                        "redirected to the Envoy sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatsInclusionPrefixes = Instance {
          Name: "sidecar.polarismesh.cn/statsInclusionPrefixes",
          Description: "Specifies the comma separated list of prefixes of the "+
                        "stats to be emitted by Envoy.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatsInclusionRegexps = Instance {
          Name: "sidecar.polarismesh.cn/statsInclusionRegexps",
          Description: "Specifies the comma separated list of regexes the stats "+
                        "should match to be emitted by Envoy.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatsInclusionSuffixes = Instance {
          Name: "sidecar.polarismesh.cn/statsInclusionSuffixes",
          Description: "Specifies the comma separated list of suffixes of the "+
                        "stats to be emitted by Envoy.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatus = Instance {
          Name: "sidecar.polarismesh.cn/status",
          Description: "Generated by Envoy sidecar injection that indicates the "+
                        "status of the operation. Includes a version hash of the "+
                        "executed template, as well as names of injected "+
                        "resources.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarUserVolume = Instance {
          Name: "sidecar.polarismesh.cn/userVolume",
          Description: "Specifies one or more user volumes (as a JSON array) to "+
                        "be added to the Envoy sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarUserVolumeMount = Instance {
          Name: "sidecar.polarismesh.cn/userVolumeMount",
          Description: "Specifies one or more user volume mounts (as a JSON "+
                        "array) to be added to the Envoy sidecar.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarStatusPort = Instance {
          Name: "status.sidecar.polarismesh.cn/port",
          Description: "Specifies the HTTP status Port for the Envoy sidecar. If "+
                        "zero, the sidecar will not provide status.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarTrafficExcludeInboundPorts = Instance {
          Name: "traffic.sidecar.polarismesh.cn/excludeInboundPorts",
          Description: "A comma separated list of inbound ports to be excluded "+
                        "from redirection to Envoy. Only applies when all inbound "+
                        "traffic (i.e. '*') is being redirected.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarTrafficExcludeOutboundIPRanges = Instance {
          Name: "traffic.sidecar.polarismesh.cn/excludeOutboundIPRanges",
          Description: "A comma separated list of IP ranges in CIDR form to be "+
                        "excluded from redirection. Only applies when all outbound "+
                        "traffic (i.e. '*') is being redirected.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarTrafficExcludeOutboundPorts = Instance {
          Name: "traffic.sidecar.polarismesh.cn/excludeOutboundPorts",
          Description: "A comma separated list of outbound ports to be excluded "+
                        "from redirection to Envoy.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarTrafficIncludeInboundPorts = Instance {
          Name: "traffic.sidecar.polarismesh.cn/includeInboundPorts",
          Description: "A comma separated list of inbound ports for which traffic "+
                        "is to be redirected to Envoy. The wildcard character '*' "+
                        "can be used to configure redirection for all ports. An "+
                        "empty list will disable all inbound redirection.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }

		SidecarTrafficIncludeOutboundIPRanges = Instance {
          Name: "traffic.sidecar.polarismesh.cn/includeOutboundIPRanges",
          Description: "A comma separated list of IP ranges in CIDR form to "+
                        "redirect to Envoy (optional). The wildcard character '*' "+
                        "can be used to redirect all outbound traffic. An empty "+
                        "list will disable all outbound redirection.",
          Hidden: false,
          Deprecated: false,
		  Resources: []ResourceTypes{ Pod, },
        }
)
