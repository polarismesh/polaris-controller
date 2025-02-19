// Copyright 2018 Istio Authors
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

package inject

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
)

type annotationValidationFunc func(value string) error

// per-sidecar policy and status
var (
	alwaysValidFunc = func(value string) error {
		return nil
	}

	annotationRegistryForMesh = map[string]annotationValidationFunc{
		annotation.SidecarInject.Name:                         alwaysValidFunc,
		annotation.SidecarStatus.Name:                         alwaysValidFunc,
		annotation.SidecarRewriteAppHTTPProbers.Name:          alwaysValidFunc,
		annotation.SidecarDiscoveryAddress.Name:               alwaysValidFunc,
		annotation.SidecarProxyImage.Name:                     alwaysValidFunc,
		annotation.SidecarProxyCPU.Name:                       alwaysValidFunc,
		annotation.SidecarProxyMemory.Name:                    alwaysValidFunc,
		annotation.SidecarInterceptionMode.Name:               alwaysValidFunc,
		annotation.SidecarBootstrapOverride.Name:              alwaysValidFunc,
		annotation.SidecarStatsInclusionPrefixes.Name:         alwaysValidFunc,
		annotation.SidecarStatsInclusionSuffixes.Name:         alwaysValidFunc,
		annotation.SidecarStatsInclusionRegexps.Name:          alwaysValidFunc,
		annotation.SidecarUserVolume.Name:                     alwaysValidFunc,
		annotation.SidecarUserVolumeMount.Name:                alwaysValidFunc,
		annotation.SidecarEnableCoreDump.Name:                 validateBool,
		annotation.SidecarStatusPort.Name:                     validateStatusPort,
		annotation.SidecarTrafficIncludeOutboundIPRanges.Name: ValidateIncludeIPRanges,
		annotation.SidecarTrafficExcludeOutboundIPRanges.Name: ValidateExcludeIPRanges,
		annotation.SidecarTrafficIncludeInboundPorts.Name:     ValidateIncludeInboundPorts,
		annotation.SidecarTrafficExcludeInboundPorts.Name:     ValidateExcludeInboundPorts,
		annotation.SidecarTrafficExcludeOutboundPorts.Name:    ValidateExcludeOutboundPorts,
	}
)

func validateMeshAnnotations(annotations map[string]string) (err error) {
	for name, value := range annotations {
		if v, ok := annotationRegistryForMesh[name]; ok {
			if e := v(value); e != nil {
				err = multierror.Append(err, fmt.Errorf("invalid value '%s' for annotation '%s': %v", value, name, e))
			}
		}
	}
	return
}

const (
	// ProxyContainerName is used by e2e integration tests for fetching logs
	ProxyContainerName = "polaris-proxy"
)

// SidecarInjectionSpec collects all container types and volumes for
// sidecar mesh injection
type SidecarInjectionSpec struct {
	// RewriteHTTPProbe indicates whether Kubernetes HTTP prober in the PodSpec
	// will be rewritten to be redirected by pilot agent.
	PodRedirectAnnot    map[string]string             `yaml:"podRedirectAnnot"`
	RewriteAppHTTPProbe bool                          `yaml:"rewriteAppHTTPProbe"`
	InitContainers      []corev1.Container            `yaml:"initContainers"`
	Containers          []corev1.Container            `yaml:"containers"`
	Volumes             []corev1.Volume               `yaml:"volumes"`
	DNSConfig           *corev1.PodDNSConfig          `yaml:"dnsConfig"`
	ImagePullSecrets    []corev1.LocalObjectReference `yaml:"imagePullSecrets"`
}

// SidecarTemplateData is the data object to which the templated
// version of `SidecarInjectionSpec` is applied.
type SidecarTemplateData struct {
	TypeMeta       *metav1.TypeMeta
	DeploymentMeta *metav1.ObjectMeta
	ObjectMeta     *metav1.ObjectMeta
	Spec           *corev1.PodSpec
	ProxyConfig    *mesh.DefaultConfig
	Values         map[string]interface{}
}

func (wh *Webhook) meshInjectRequired(p *podDataInfo) bool {
	podSpec := &p.podObject.Spec
	metadata := &p.podObject.ObjectMeta
	templateConfig := p.injectTemplateConfig
	podName := p.podName
	// Skip injection when host networking is enabled. The problem is
	// that the iptable changes are assumed to be within the pod when,
	// in fact, they are changing the routing at the host level. This
	// often results in routing failures within a node which can
	// affect the network provider within the cluster causing
	// additional pod failures.
	if podSpec.HostNetwork {
		return false
	}

	// skip special kubernetes system namespaces
	for _, namespace := range ignoredNamespaces {
		if metadata.Namespace == namespace {
			return false
		}
	}

	annos := metadata.GetAnnotations()
	if annos == nil {
		annos = map[string]string{}
	}

	var useDefault bool
	var inject bool
	switch strings.ToLower(annos[annotation.SidecarInject.Name]) {
	// http://yaml.org/type/bool.html
	case "y", "yes", "true", "on", "enable", "enabled":
		inject = true
	case "n", "no", "false", "off", "disable", "disabled":
		inject = false
	case "":
		useDefault = true
	}

	// If an annotation is not explicitly given, check the LabelSelectors, starting with NeverInject
	if useDefault {
		for _, neverSelector := range templateConfig.NeverInjectSelector {
			selector, err := metav1.LabelSelectorAsSelector(&neverSelector)
			if err != nil {
				log.InjectScope().Warnf("Invalid selector for NeverInjectSelector: %v (%v)", neverSelector, err)
			} else if !selector.Empty() && selector.Matches(labels.Set(metadata.Labels)) {
				log.InjectScope().Infof("Explicitly disabling injection for pod %s/%s due to pod labels "+
					"matching NeverInjectSelector templateConfig map entry.", metadata.Namespace, podName)
				inject = false
				useDefault = false
				break
			}
		}
	}

	// If there's no annotation nor a NeverInjectSelector, check the AlwaysInject one
	if useDefault {
		for _, alwaysSelector := range templateConfig.AlwaysInjectSelector {
			selector, err := metav1.LabelSelectorAsSelector(&alwaysSelector)
			if err != nil {
				log.InjectScope().Warnf("Invalid selector for AlwaysInjectSelector: %v (%v)", alwaysSelector, err)
			} else if !selector.Empty() && selector.Matches(labels.Set(metadata.Labels)) {
				log.InjectScope().Infof("Explicitly enabling injection for pod %s/%s due to pod labels "+
					" matching AlwaysInjectSelector templateConfig map entry.", metadata.Namespace, podName)
				inject = true
				useDefault = false
				break
			}
		}
	}

	var required bool
	switch templateConfig.Policy {
	default: // InjectionPolicyOff
		log.InjectScope().Errorf("Illegal value for autoInject:%s, must be one of [%s,%s]. Auto injection disabled!",
			templateConfig.Policy, config.InjectionPolicyDisabled, config.InjectionPolicyEnabled)
		required = false
	case config.InjectionPolicyDisabled:
		if useDefault {
			required = false
		} else {
			required = inject
		}
	case config.InjectionPolicyEnabled:
		if useDefault {
			required = true
		} else {
			required = inject
		}
	}

	// Build a log message for the annotations.
	annotationStr := ""
	for name := range annotationRegistryForMesh {
		value, ok := annos[name]
		if !ok {
			value = "(unset)"
		}
		annotationStr += fmt.Sprintf("%s:%s ", name, value)
	}

	log.InjectScope().Infof("Sidecar injection for %v/%v: namespacePolicy:%v useDefault:%v inject:%v required:%v %s",
		metadata.Namespace,
		podName,
		templateConfig.Policy,
		useDefault,
		inject,
		required,
		annotationStr)

	return required
}
