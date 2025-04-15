package config

import (
	"crypto/tls"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
)

type SafeTemplateConfig struct {
	mu sync.RWMutex
	// envoy mesh 场景下的 sidecar 注入配置
	sidecarMeshTemplate        *TemplateConfig
	sidecarMeshTemplateVersion string
	// dns 场景下的 sidecar 注入配置
	sidecarDnsTemplate        *TemplateConfig
	sidecarDnsTemplateVersion string
	// java agent 场景下的注入配置
	sidecarJavaAgentTemplate        *TemplateConfig
	sidecarJavaAgentTemplateVersion string
	// 北极星proxy配置
	meshEnvoyConfig *mesh.MeshEnvoyConfig
	valuesConfig    string
	cert            *tls.Certificate
}

// TemplateConfig specifies the sidecar injection configuration This includes
// the sidecar template and cluster-side injection policy. It is used
// by kube-inject, sidecar injector, and http endpoint.
type TemplateConfig struct {
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

// NewTemplateConfig 创建模板配置
func NewTemplateConfig(p TemplateFileConfig) (*SafeTemplateConfig, error) {
	injectConf, err := loadConfig(p)
	if err != nil {
		log.InjectScope().Errorf("Failed to load inject config: %v", err)
		return nil, err
	}
	return &SafeTemplateConfig{
		sidecarMeshTemplate:             injectConf.MeshInjectConf,
		sidecarMeshTemplateVersion:      sidecarTemplateVersionHash(injectConf.MeshInjectConf.Template),
		sidecarDnsTemplate:              injectConf.DnsInjectConf,
		sidecarDnsTemplateVersion:       sidecarTemplateVersionHash(injectConf.DnsInjectConf.Template),
		sidecarJavaAgentTemplate:        injectConf.JavaAgentInjectConf,
		sidecarJavaAgentTemplateVersion: sidecarTemplateVersionHash(injectConf.JavaAgentInjectConf.Template),
		meshEnvoyConfig:                 injectConf.MeshEnvoyConf,
		valuesConfig:                    injectConf.ValuesConf,
		cert:                            injectConf.CertPair,
	}, nil
}

// UpdateTemplateConfig 更新模板配置
func (tc *SafeTemplateConfig) UpdateTemplateConfig(p TemplateFileConfig) error {
	injectConf, err := loadConfig(p)
	if err != nil {
		log.InjectScope().Errorf("Failed to load inject config: %v", err)
		return err
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.sidecarMeshTemplate = injectConf.MeshInjectConf
	tc.sidecarMeshTemplateVersion = sidecarTemplateVersionHash(injectConf.MeshInjectConf.Template)
	tc.sidecarDnsTemplate = injectConf.DnsInjectConf
	tc.sidecarDnsTemplateVersion = sidecarTemplateVersionHash(injectConf.DnsInjectConf.Template)
	tc.sidecarJavaAgentTemplate = injectConf.JavaAgentInjectConf
	tc.sidecarJavaAgentTemplateVersion = sidecarTemplateVersionHash(injectConf.JavaAgentInjectConf.Template)
	tc.meshEnvoyConfig = injectConf.MeshEnvoyConf
	tc.valuesConfig = injectConf.ValuesConf
	tc.cert = injectConf.CertPair
	return nil
}

// GetSidecarMeshTemplateAndVersion 获取sidecar mesh 注入配置模板
func (tc *SafeTemplateConfig) GetSidecarMeshTemplateAndVersion() (*TemplateConfig, string) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.sidecarMeshTemplate, tc.sidecarMeshTemplateVersion
}

// GetSidecarDnsTemplateAndVersion 获取sidecar dns 注入配置模板
func (tc *SafeTemplateConfig) GetSidecarDnsTemplateAndVersion() (*TemplateConfig, string) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.sidecarDnsTemplate, tc.sidecarDnsTemplateVersion
}

// GetSidecarJavaAgentTemplateAndVersion 获取sidecar java agent 注入配置模板
func (tc *SafeTemplateConfig) GetSidecarJavaAgentTemplateAndVersion() (*TemplateConfig, string) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.sidecarJavaAgentTemplate, tc.sidecarJavaAgentTemplateVersion
}

// GetCert 获取证书
func (tc *SafeTemplateConfig) GetCert(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()
	return tc.cert, nil
}

// GetMeshEnvoyConfig returns the mesh envoy configuration in a thread-safe way
func (s *SafeTemplateConfig) GetMeshEnvoyConfig() *mesh.MeshEnvoyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meshEnvoyConfig
}

// SetMeshEnvoyConfig sets the mesh envoy configuration in a thread-safe way
func (s *SafeTemplateConfig) SetMeshEnvoyConfigWithKV(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meshEnvoyConfig.SetProxyMetadataWithKV(k, v)
}

// GetValuesConfig returns the values configuration in a thread-safe way
func (s *SafeTemplateConfig) GetValuesConfig() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.valuesConfig
}
