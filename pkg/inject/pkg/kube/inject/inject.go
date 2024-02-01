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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
	"github.com/polarismesh/polaris-controller/pkg/util"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

type annotationValidationFunc func(value string) error

// per-sidecar policy and status
var (
	alwaysValidFunc = func(value string) error {
		return nil
	}

	annotationRegistry = map[string]annotationValidationFunc{
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

func validateAnnotations(annotations map[string]string) (err error) {
	for name, value := range annotations {
		if v, ok := annotationRegistry[name]; ok {
			if e := v(value); e != nil {
				err = multierror.Append(err, fmt.Errorf("invalid value '%s' for annotation '%s': %v", value, name, e))
			}
		}
	}
	return
}

// InjectionPolicy determines the policy for injecting the
// sidecar proxy into the watched namespace(s).
type InjectionPolicy string

const (
	// InjectionPolicyDisabled specifies that the sidecar injector
	// will not inject the sidecar into resources by default for the
	// namespace(s) being watched. Resources can enable injection
	// using the "sidecar.polarismesh.cn/inject" annotation with value of
	// true.
	InjectionPolicyDisabled InjectionPolicy = "disabled"

	// InjectionPolicyEnabled specifies that the sidecar injector will
	// inject the sidecar into resources by default for the
	// namespace(s) being watched. Resources can disable injection
	// using the "sidecar.polarismesh.cn/inject" annotation with value of
	// false.
	InjectionPolicyEnabled InjectionPolicy = "enabled"
)

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

// Config specifies the sidecar injection configuration This includes
// the sidecar template and cluster-side injection policy. It is used
// by kube-inject, sidecar injector, and http endpoint.
type Config struct {
	Policy InjectionPolicy `json:"policy"`

	// Template is the templated version of `SidecarInjectionSpec` prior to
	// expansion over the `SidecarTemplateData`.
	Template string `json:"template"`

	// NeverInjectSelector: Refuses the injection on pods whose labels match this selector.
	// It's an array of label selectors, that will be OR'ed, meaning we will iterate
	// over it and stop at the first match
	// Takes precedence over AlwaysInjectSelector.
	NeverInjectSelector []metav1.LabelSelector `json:"neverInjectSelector"`

	// AlwaysInjectSelector: Forces the injection on pods whose labels match this selector.
	// It's an array of label selectors, that will be OR'ed, meaning we will iterate
	// over it and stop at the first match
	AlwaysInjectSelector []metav1.LabelSelector `json:"alwaysInjectSelector"`

	// InjectedAnnotations are additional annotations that will be added to the pod spec after injection
	// This is primarily to support PSP annotations.
	InjectedAnnotations map[string]string `json:"injectedAnnotations"`
}

func validateCIDRList(cidrs string) error {
	if len(cidrs) > 0 {
		for _, cidr := range strings.Split(cidrs, ",") {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("failed parsing cidr '%s': %v", cidr, err)
			}
		}
	}
	return nil
}

func splitPorts(portsString string) []string {
	return strings.Split(portsString, ",")
}

func parsePort(portStr string) (int, error) {
	port, err := strconv.ParseUint(strings.TrimSpace(portStr), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("failed parsing port '%d': %v", port, err)
	}
	return int(port), nil
}

func parsePorts(portsString string) ([]int, error) {
	portsString = strings.TrimSpace(portsString)
	ports := make([]int, 0)
	if len(portsString) > 0 {
		for _, portStr := range splitPorts(portsString) {
			port, err := parsePort(portStr)
			if err != nil {
				return nil, fmt.Errorf("failed parsing port '%d': %v", port, err)
			}
			ports = append(ports, port)
		}
	}
	return ports, nil
}

func validatePortList(parameterName, ports string) error {
	if _, err := parsePorts(ports); err != nil {
		return fmt.Errorf("%s invalid: %v", parameterName, err)
	}
	return nil
}

// ValidateIncludeIPRanges validates the includeIPRanges parameter
func ValidateIncludeIPRanges(ipRanges string) error {
	if ipRanges != "*" {
		if e := validateCIDRList(ipRanges); e != nil {
			return fmt.Errorf("includeIPRanges invalid: %v", e)
		}
	}
	return nil
}

// ValidateExcludeIPRanges validates the excludeIPRanges parameter
func ValidateExcludeIPRanges(ipRanges string) error {
	if e := validateCIDRList(ipRanges); e != nil {
		return fmt.Errorf("excludeIPRanges invalid: %v", e)
	}
	return nil
}

// ValidateIncludeInboundPorts validates the includeInboundPorts parameter
func ValidateIncludeInboundPorts(ports string) error {
	if ports != "*" {
		return validatePortList("includeInboundPorts", ports)
	}
	return nil
}

// ValidateExcludeInboundPorts validates the excludeInboundPorts parameter
func ValidateExcludeInboundPorts(ports string) error {
	return validatePortList("excludeInboundPorts", ports)
}

// ValidateExcludeOutboundPorts validates the excludeOutboundPorts parameter
func ValidateExcludeOutboundPorts(ports string) error {
	return validatePortList("excludeOutboundPorts", ports)
}

// validateStatusPort validates the statusPort parameter
func validateStatusPort(port string) error {
	if _, e := parsePort(port); e != nil {
		return fmt.Errorf("excludeInboundPorts invalid: %v", e)
	}
	return nil
}

// validateUInt32 validates that the given annotation value is a positive integer.
func validateUInt32(value string) error {
	_, err := strconv.ParseUint(value, 10, 32)
	return err
}

// validateBool validates that the given annotation value is a boolean.
func validateBool(value string) error {
	_, err := strconv.ParseBool(value)
	return err
}

// getSidecarMode 获取 sidecar 注入模式
func (wh *Webhook) getSidecarMode(namespace string, pod *corev1.Pod) utils.SidecarMode {
	// 这里主要是处理北极星 sidecar, 优先级: pod.annotations > namespace.labels > configmap
	if val, ok := pod.Annotations["polarismesh.cn/javaagent"]; ok && val == "true" {
		log.InjectScope().Infof("inject pod namespace %q mode is java agent", namespace)
		return utils.SidecarForJavaAgent
	}
	sidecarMode := ""
	// 1. pod.annotations
	if val, ok := pod.Annotations[utils.PolarisSidecarMode]; ok {
		sidecarMode = val
		return utils.ParseSidecarMode(sidecarMode)
	}

	// 2. namespace.labels
	ns, err := wh.k8sClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		// 如果出现异常，则就采用配置文件中的 Sidecar 的注入模式
		log.InjectScope().Errorf("get pod namespace %q failed: %v", namespace, err)
	} else {
		if val, ok := ns.Labels[utils.PolarisSidecarModeLabel]; ok {
			sidecarMode = val
		}
		return utils.ParseSidecarMode(sidecarMode)
	}

	return wh.defaultSidecarMode
}

func (wh *Webhook) injectRequired(ignored []string, config *Config, podSpec *corev1.PodSpec,
	metadata *metav1.ObjectMeta) bool {
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
	for _, namespace := range ignored {
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
	case "y", "yes", "true", "on":
		inject = true
	case "n", "no", "false", "off":
		inject = false
	case "":
		useDefault = true
	}

	// If an annotation is not explicitly given, check the LabelSelectors, starting with NeverInject
	if useDefault {
		for _, neverSelector := range config.NeverInjectSelector {
			selector, err := metav1.LabelSelectorAsSelector(&neverSelector)
			if err != nil {
				log.InjectScope().Warnf("Invalid selector for NeverInjectSelector: %v (%v)", neverSelector, err)
			} else if !selector.Empty() && selector.Matches(labels.Set(metadata.Labels)) {
				log.InjectScope().Infof("Explicitly disabling injection for pod %s/%s due to pod labels "+
					"matching NeverInjectSelector config map entry.",
					metadata.Namespace, potentialPodName(metadata))
				inject = false
				useDefault = false
				break
			}
		}
	}

	// If there's no annotation nor a NeverInjectSelector, check the AlwaysInject one
	if useDefault {
		for _, alwaysSelector := range config.AlwaysInjectSelector {
			selector, err := metav1.LabelSelectorAsSelector(&alwaysSelector)
			if err != nil {
				log.InjectScope().Warnf("Invalid selector for AlwaysInjectSelector: %v (%v)", alwaysSelector, err)
			} else if !selector.Empty() && selector.Matches(labels.Set(metadata.Labels)) {
				log.InjectScope().Infof("Explicitly enabling injection for pod %s/%s due to pod labels "+
					" matching AlwaysInjectSelector config map entry.",
					metadata.Namespace, potentialPodName(metadata))
				inject = true
				useDefault = false
				break
			}
		}
	}

	var required bool
	switch config.Policy {
	default: // InjectionPolicyOff
		log.InjectScope().Errorf("Illegal value for autoInject:%s, must be one of [%s,%s]. Auto injection disabled!",
			config.Policy, InjectionPolicyDisabled, InjectionPolicyEnabled)
		required = false
	case InjectionPolicyDisabled:
		if useDefault {
			required = false
		} else {
			required = inject
		}
	case InjectionPolicyEnabled:
		if useDefault {
			required = true
		} else {
			required = inject
		}
	}

	// Build a log message for the annotations.
	annotationStr := ""
	for name := range annotationRegistry {
		value, ok := annos[name]
		if !ok {
			value = "(unset)"
		}
		annotationStr += fmt.Sprintf("%s:%s ", name, value)
	}

	log.InjectScope().Infof("Sidecar injection for %v/%v: namespacePolicy:%v useDefault:%v inject:%v required:%v %s",
		metadata.Namespace,
		potentialPodName(metadata),
		config.Policy,
		useDefault,
		inject,
		required,
		annotationStr)

	return required
}

func formatDuration(in *types.Duration) string {
	dur, err := types.DurationFromProto(in)
	if err != nil {
		return "1s"
	}
	return dur.String()
}

func isset(m map[string]string, key string) bool {
	_, ok := m[key]
	return ok
}

func openTlsMode(m map[string]string, key string) bool {
	if len(m) == 0 {
		return false
	}

	v, ok := m[key]
	if !ok {
		return false
	}

	return v != util.MTLSModeNone
}

func directory(filepath string) string {
	dir, _ := path.Split(filepath)
	return dir
}

func flippedContains(needle, haystack string) bool {
	return strings.Contains(haystack, needle)
}

// InjectionData renders sidecarTemplate with valuesConfig.
func InjectionData(sidecarTemplate, valuesConfig, version string, typeMetadata *metav1.TypeMeta,
	deploymentMetadata *metav1.ObjectMeta, spec *corev1.PodSpec,
	metadata *metav1.ObjectMeta, proxyConfig *mesh.DefaultConfig) (
	*SidecarInjectionSpec, map[string]string, string, error) {
	// If DNSPolicy is not ClusterFirst, the Envoy sidecar may not able to connect to polaris.
	if spec.DNSPolicy != "" && spec.DNSPolicy != corev1.DNSClusterFirst {
		podName := potentialPodName(metadata)
		log.InjectScope().Errorf("[Webhook] %q's DNSPolicy is not %q. The Envoy sidecar may not able to "+
			" connect to PolarisMesh Control Plane",
			metadata.Namespace+"/"+podName, corev1.DNSClusterFirst)
		return nil, nil, "", nil
	}

	if err := validateAnnotations(metadata.GetAnnotations()); err != nil {
		log.InjectScope().Errorf("[Webhook] injection failed due to invalid annotations: %v", err)
		return nil, nil, "", err
	}

	values := map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(valuesConfig), &values); err != nil {
		log.InjectScope().Infof("[Webhook] failed to parse values config: %v [%v]\n", err, valuesConfig)
		return nil, nil, "", multierror.Prefix(err, "could not parse configuration values:")
	}

	injectAnnonations := map[string]string{}
	// 设置需要注入到 envoy 的 metadata
	// 强制开启 XDS On-Demand 能力
	envoyMetadata := map[string]string{}
	// 这里负责将需要额外塞入的 annonation 数据进行返回
	if len(metadata.Annotations) != 0 {
		tlsMode := util.MTLSModeNone
		if mode, ok := metadata.Annotations[util.PolarisTLSMode]; ok {
			mode = strings.ToLower(mode)
			if mode == util.MTLSModeStrict || mode == util.MTLSModePermissive {
				tlsMode = mode
			}
		}
		injectAnnonations[util.PolarisTLSMode] = tlsMode
		envoyMetadata[util.PolarisTLSMode] = tlsMode

		// 按需加载能力需要显示开启
		if val, ok := metadata.Annotations["sidecar.polarismesh.cn/openOnDemand"]; ok {
			envoyMetadata["sidecar.polarismesh.cn/openOnDemand"] = val
			proxyConfig.ProxyMetadata["OPEN_DEMAND"] = val
		}
	}
	// 这里需要将 sidecar 所属的服务信息注入到 annonation 中，方便下发到 envoy 的 bootstrap.yaml 中
	// 命名空间可以不太强要求用户设置，大部份场景都是保持和 kubernetes 部署所在的 namespace 保持一致的
	if _, ok := metadata.Annotations[utils.SidecarNamespaceName]; !ok {
		injectAnnonations[utils.SidecarNamespaceName] = metadata.Namespace
		envoyMetadata[utils.SidecarNamespaceName] = metadata.Namespace
	}
	if svcName, ok := metadata.Annotations[utils.SidecarServiceName]; !ok {
		// 如果官方注解没有查询到，那就默认按照 istio 的约定，从 labels 中读取 app 这个标签的 value 作为服务名
		if val, ok := metadata.Labels["app"]; ok {
			injectAnnonations[utils.SidecarServiceName] = val
			envoyMetadata[utils.SidecarServiceName] = val
		}
	} else {
		envoyMetadata[utils.SidecarServiceName] = svcName
	}

	for k, v := range metadata.Labels {
		envoyMetadata[k] = v
	}
	injectAnnonations[utils.SidecarEnvoyMetadata] = fmt.Sprintf("%q", toJSON(envoyMetadata))
	metadataCopy := metadata.DeepCopy()
	metadataCopy.Annotations = injectAnnonations
	data := SidecarTemplateData{
		TypeMeta:       typeMetadata,
		DeploymentMeta: deploymentMetadata,
		ObjectMeta:     metadataCopy,
		Spec:           spec,
		ProxyConfig:    proxyConfig,
		Values:         values,
	}

	funcMap := template.FuncMap{
		"formatDuration":      formatDuration,
		"isset":               isset,
		"excludeInboundPort":  excludeInboundPort,
		"includeInboundPorts": includeInboundPorts,
		"kubevirtInterfaces":  kubevirtInterfaces,
		"applicationPorts":    applicationPorts,
		"annotation":          getAnnotation,
		"valueOrDefault":      valueOrDefault,
		"toJSON":              toJSON,
		"toJson":              toJSON, // Used by, e.g. Istio 1.0.5 template sidecar-injector-configmap.yaml
		"fromJSON":            fromJSON,
		"structToJSON":        structToJSON,
		"protoToJSON":         protoToJSON,
		"toYaml":              toYaml,
		"indent":              indent,
		"directory":           directory,
		"contains":            flippedContains,
		"toLower":             strings.ToLower,
		"openTlsMode":         openTlsMode,
	}

	// Allows the template to use env variables from istiod.
	// Istiod will use a custom template, without 'values.yaml', and the pod will have
	// an optional 'vendor' configmap where additional settings can be defined.
	funcMap["env"] = func(key string, def string) string {
		val := os.Getenv(key)
		if val == "" {
			return def
		}
		return val
	}

	// Need to use FuncMap and SidecarTemplateData context
	funcMap["render"] = func(template string) string {
		bbuf, err := parseTemplate(template, funcMap, data)
		if err != nil {
			return ""
		}

		return bbuf.String()
	}

	bbuf, err := parseTemplate(sidecarTemplate, funcMap, data)
	if err != nil {
		return nil, nil, "", err
	}

	var sic SidecarInjectionSpec
	if err := yaml.Unmarshal(bbuf.Bytes(), &sic); err != nil {
		// This usually means an invalid injector template; we can't check
		// the template itself because it is merely a string.
		log.InjectScope().Warnf("Failed to unmarshal template %v \n%s", err, bbuf.String())
		return nil, nil, "", multierror.Prefix(err, "failed parsing injected YAML (check sidecar injector configuration):")
	}

	// set sidecar --concurrency
	applyConcurrency(sic.Containers)

	status := &SidecarInjectionStatus{Version: version}
	for _, c := range sic.InitContainers {
		status.InitContainers = append(status.InitContainers, corev1.Container{
			Name: c.Name,
		})
	}
	for _, c := range sic.Containers {
		status.Containers = append(status.Containers, corev1.Container{
			Name: c.Name,
		})
	}
	for _, c := range sic.Volumes {
		status.Volumes = append(status.Volumes, corev1.Volume{
			Name: c.Name,
		})
	}
	for _, c := range sic.ImagePullSecrets {
		status.ImagePullSecrets = append(status.ImagePullSecrets, corev1.LocalObjectReference{
			Name: c.Name,
		})
	}
	statusAnnotationValue, err := json.Marshal(status)
	if err != nil {
		return nil, nil, "", fmt.Errorf("error encoded injection status: %v", err)
	}
	return &sic, injectAnnonations, string(statusAnnotationValue), err
}

func parseTemplate(tmplStr string, funcMap map[string]interface{}, data SidecarTemplateData) (bytes.Buffer, error) {
	var tmpl bytes.Buffer
	temp := template.New("inject")
	t, err := temp.Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		log.InjectScope().Infof("Failed to parse template: %v %v\n", err, tmplStr)
		return bytes.Buffer{}, err
	}
	if err := t.Execute(&tmpl, &data); err != nil {
		log.InjectScope().Infof("Invalid template: %v %v\n", err, tmplStr)
		return bytes.Buffer{}, err
	}

	return tmpl, nil
}

// FromRawToObject is used to convert from raw to the runtime object
func FromRawToObject(raw []byte) (runtime.Object, error) {
	var typeMeta metav1.TypeMeta
	if err := yaml.Unmarshal(raw, &typeMeta); err != nil {
		return nil, err
	}

	gvk := schema.FromAPIVersionAndKind(typeMeta.APIVersion, typeMeta.Kind)
	obj, err := injectScheme.New(gvk)
	if err != nil {
		return nil, err
	}
	if err = yaml.Unmarshal(raw, obj); err != nil {
		return nil, err
	}

	return obj, nil
}

func getPortsForContainer(container corev1.Container) []string {
	parts := make([]string, 0)
	for _, p := range container.Ports {
		if p.Protocol == corev1.ProtocolUDP || p.Protocol == corev1.ProtocolSCTP {
			continue
		}
		parts = append(parts, strconv.Itoa(int(p.ContainerPort)))
	}
	return parts
}

func getContainerPorts(containers []corev1.Container, shouldIncludePorts func(corev1.Container) bool) string {
	parts := make([]string, 0)
	for _, c := range containers {
		if shouldIncludePorts(c) {
			parts = append(parts, getPortsForContainer(c)...)
		}
	}

	return strings.Join(parts, ",")
}

// this function is no longer used by the template but kept around for backwards compatibility
func applicationPorts(containers []corev1.Container) string {
	return getContainerPorts(containers, func(c corev1.Container) bool {
		return c.Name != ProxyContainerName
	})
}

func includeInboundPorts(containers []corev1.Container) string {
	// Include the ports from all containers in the deployment.
	return getContainerPorts(containers, func(corev1.Container) bool { return true })
}

func kubevirtInterfaces(s string) string {
	return s
}

func structToJSON(v interface{}) string {
	if v == nil {
		return "{}"
	}

	ba, err := json.Marshal(v)
	if err != nil {
		log.InjectScope().Warnf("Unable to marshal %v", v)
		return "{}"
	}

	return string(ba)
}

func protoToJSON(v proto.Message) string {
	if v == nil {
		return "{}"
	}

	m := jsonpb.Marshaler{}
	ba, err := m.MarshalToString(v)
	if err != nil {
		log.InjectScope().Warnf("Unable to marshal %v: %v", v, err)
		return "{}"
	}

	return ba
}

func toJSON(m map[string]string) string {
	if m == nil {
		return "{}"
	}

	ba, err := json.Marshal(m)
	if err != nil {
		log.InjectScope().Warnf("Unable to marshal %v", m)
		return "{}"
	}

	return string(ba)
}

func fromJSON(j string) interface{} {
	var m interface{}
	err := json.Unmarshal([]byte(j), &m)
	if err != nil {
		log.InjectScope().Warnf("Unable to unmarshal %s", j)
		return "{}"
	}

	log.InjectScope().Warnf("%v", m)
	return m
}

func indent(spaces int, source string) string {
	res := strings.Split(source, "\n")
	for i, line := range res {
		if i > 0 {
			res[i] = fmt.Sprintf(fmt.Sprintf("%% %ds%%s", spaces), "", line)
		}
	}
	return strings.Join(res, "\n")
}

func toYaml(value interface{}) string {
	y, err := yaml.Marshal(value)
	if err != nil {
		log.InjectScope().Warnf("Unable to marshal %v", value)
		return ""
	}

	return string(y)
}

func getAnnotation(meta metav1.ObjectMeta, name string, defaultValue interface{}) string {
	value, ok := meta.Annotations[name]
	if !ok {
		value = fmt.Sprint(defaultValue)
	}
	return value
}

func excludeInboundPort(port interface{}, excludedInboundPorts string) string {
	portStr := strings.TrimSpace(fmt.Sprint(port))
	if len(portStr) == 0 || portStr == "0" {
		// Nothing to do.
		return excludedInboundPorts
	}

	// Exclude the readiness port if not already excluded.
	ports := splitPorts(excludedInboundPorts)
	outPorts := make([]string, 0, len(ports))
	for _, port := range ports {
		if port == portStr {
			// The port is already excluded.
			return excludedInboundPorts
		}
		port = strings.TrimSpace(port)
		if len(port) > 0 {
			outPorts = append(outPorts, port)
		}
	}

	// The port was not already excluded - exclude it now.
	outPorts = append(outPorts, portStr)
	return strings.Join(outPorts, ",")
}

func valueOrDefault(value interface{}, defaultValue interface{}) interface{} {
	if value == "" || value == nil {
		return defaultValue
	}
	return value
}

// SidecarInjectionStatus contains basic information about the
// injected sidecar. This includes the names of added containers and
// volumes.
type SidecarInjectionStatus struct {
	Version          string                        `json:"version"`
	InitContainers   []corev1.Container            `json:"initContainers"`
	Containers       []corev1.Container            `json:"containers"`
	Volumes          []corev1.Volume               `json:"volumes"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets"`
}

// helper function to generate a template version identifier from a
// hash of the un-executed template contents.
func sidecarTemplateVersionHash(in string) string {
	hash := sha256.Sum256([]byte(in))
	return hex.EncodeToString(hash[:])
}

func potentialPodName(metadata *metav1.ObjectMeta) string {
	if metadata.Name != "" {
		return metadata.Name
	}
	if metadata.GenerateName != "" {
		return metadata.GenerateName + "***** (actual name not yet known)"
	}
	return ""
}

// rewriteCniPodSpec will check if values from the sidecar injector Helm
// values need to be inserted as Pod annotations so the CNI will apply
// the proper redirection rules.
func rewriteCniPodSpec(annotations map[string]string, spec *SidecarInjectionSpec) {
	if spec == nil {
		return
	}
	if len(spec.PodRedirectAnnot) == 0 {
		return
	}
	for k := range annotationRegistry {
		if spec.PodRedirectAnnot[k] != "" {
			if annotations[k] == spec.PodRedirectAnnot[k] {
				continue
			}
			annotations[k] = spec.PodRedirectAnnot[k]
		}
	}
}
