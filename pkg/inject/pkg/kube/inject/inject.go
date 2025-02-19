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
	utils "github.com/polarismesh/polaris-controller/pkg/util"
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
		utils.PolarisInjectionKey:                             alwaysValidFunc,
	}

	annotationRegistryForDns = map[string]annotationValidationFunc{
		annotation.SidecarInject.Name: alwaysValidFunc,
		annotation.SidecarStatus.Name: alwaysValidFunc,
		utils.PolarisInjectionKey:     alwaysValidFunc,
	}

	annotationRegistryForJavaAgent = map[string]annotationValidationFunc{
		utils.CustomJavaAgentVersion:                alwaysValidFunc,
		utils.CustomJavaAgentPluginFramework:        alwaysValidFunc,
		utils.CustomJavaAgentPluginFrameworkVersion: alwaysValidFunc,
		utils.CustomJavaAgentPluginConfig:           alwaysValidFunc,
		annotation.SidecarInject.Name:               alwaysValidFunc,
		annotation.SidecarStatus.Name:               alwaysValidFunc,
		utils.PolarisInjectionKey:                   alwaysValidFunc,
	}
)

func validateAnnotations(injectMode utils.SidecarMode, annotations map[string]string) (err error) {
	annotationRegistry := getAnnotationRegistry(injectMode)
	for name, value := range annotations {
		if v, ok := annotationRegistry[name]; ok {
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

// 是否需要注入, 规则参考istio官方定义
// https://istio.io/latest/zh/docs/ops/common-problems/injection/
func (wh *Webhook) requireInject(p *podDataInfo) bool {
	pod := p.podObject
	templateConfig := p.injectTemplateConfig

	if pod.Spec.HostNetwork && (p.injectMode == utils.SidecarForMesh || p.injectMode == utils.SidecarForDns) {
		return false
	}

	if isIgnoredNamespace(pod.Namespace) {
		return false
	}

	annotations := pod.GetAnnotations()
	annotationStr := buildAnnotationLogString(p.injectMode, annotations)
	// annotations为最高优先级
	injectionRequested, useDefaultPolicy := parseInjectionAnnotation(pod.GetAnnotations())
	log.InjectScope().Infof("parseInjectionAnnotation for %s/%s: UseDefault=%v injectionRequested=%v annotations=%s",
		pod.Namespace, pod.Name, useDefaultPolicy, injectionRequested, annotationStr)

	// Labels为次优先级
	if useDefaultPolicy {
		injectionRequested, useDefaultPolicy = evaluateSelectors(pod, templateConfig, p.podName)
		log.InjectScope().Infof("evaluateSelectors for %s/%s: UseDefault=%v injectionRequested=%v",
			pod.Namespace, pod.Name, useDefaultPolicy, injectionRequested)
	}

	// Policy为最低优先级
	required := determineInjectionRequirement(templateConfig.Policy, useDefaultPolicy, injectionRequested)
	log.InjectScope().Infof("determineInjectionRequirement for %s/%s: Policy=%s UseDefault=%v Requested=%v Required=%v",
		pod.Namespace, pod.Name, templateConfig.Policy, useDefaultPolicy, injectionRequested, required)

	return required
}

func isIgnoredNamespace(namespace string) bool {
	for _, ignored := range ignoredNamespaces {
		if namespace == ignored {
			return true
		}
	}
	return false
}

// 解析Annotation, 用于注解位置的黑白名单功能
func parseInjectionAnnotation(annotations map[string]string) (requested bool, useDefault bool) {
	var value string
	newFlag, newExists := annotations[utils.PolarisInjectionKey]
	if newExists {
		value = strings.ToLower(newFlag)
	} else {
		if flag, ok := annotations[annotation.SidecarInject.Name]; ok {
			value = strings.ToLower(flag)
		}
	}
	switch value {
	case "y", "yes", "true", "on", "enable", "enabled":
		// annotation白名单, 显示开启
		return true, false
	case "n", "no", "false", "off", "disable", "disabled":
		// annotation黑名单, 显示关闭
		return false, false
	default: // including empty string
		return false, true
	}
}

// 检查label的黑白名单功能
func evaluateSelectors(pod *corev1.Pod, config *config.TemplateConfig, podName string) (bool, bool) {
	// Check NeverInject selectors first
	if inject, ok := checkSelectors(pod, config.NeverInjectSelector, "NeverInjectSelector", podName, false); ok {
		return inject, false
	}

	// Then check AlwaysInject selectors
	if inject, ok := checkSelectors(pod, config.AlwaysInjectSelector, "AlwaysInjectSelector", podName, true); ok {
		return inject, false
	}

	return false, true
}

func checkSelectors(pod *corev1.Pod, selectors []metav1.LabelSelector, selectorType string, podName string,
	allowInject bool) (bool, bool) {
	for _, selector := range selectors {
		ls, err := metav1.LabelSelectorAsSelector(&selector)
		if err != nil {
			log.InjectScope().Warnf("Invalid %s: %v (%v)", selectorType, selector, err)
			continue // Continue checking other selectors
		}

		if !ls.Empty() && ls.Matches(labels.Set(pod.Labels)) {
			log.InjectScope().Infof("Pod %s/%s: %s matched labels %v",
				pod.Namespace, podName, selectorType, pod.Labels)
			return allowInject, true
		}
	}
	return false, false
}

func determineInjectionRequirement(policy config.InjectionPolicy, useDefault bool, requested bool) bool {
	switch policy {
	case config.InjectionPolicyEnabled:
		if useDefault {
			return true
		}
		return requested
	case config.InjectionPolicyDisabled:
		if useDefault {
			return false
		}
		return requested
	default:
		log.InjectScope().Errorf("Invalid injection policy: %s. Valid values: [%s, %s]",
			policy, config.InjectionPolicyDisabled, config.InjectionPolicyEnabled)
		return false
	}
}

// buildAnnotationLogString 构建注解日志字符串，格式为"key:value key2:value2"
func buildAnnotationLogString(injectMode utils.SidecarMode, annotations map[string]string) string {
	registry := getAnnotationRegistry(injectMode)
	var buf strings.Builder
	for name := range registry {
		value, ok := annotations[name]
		if !ok {
			value = "(unset)"
		}
		buf.WriteString(fmt.Sprintf("%s:%s ", name, value))
	}
	return strings.TrimSpace(buf.String())
}

func getAnnotationRegistry(injectMode utils.SidecarMode) map[string]annotationValidationFunc {
	var registry map[string]annotationValidationFunc
	switch injectMode {
	case utils.SidecarForMesh:
		registry = annotationRegistryForMesh
	case utils.SidecarForDns:
		registry = annotationRegistryForDns
	case utils.SidecarForJavaAgent:
		registry = annotationRegistryForJavaAgent
	}
	return registry
}
