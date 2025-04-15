package config

import (
	"crypto/sha256"
	"crypto/tls"
	"os"
	"strings"

	gyaml "github.com/ghodss/yaml"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
)

type TemplateFileConfig struct {
	// MeshConfigFile 处理 polaris-sidecar 运行模式为 mesh 的配置文件
	MeshConfigFile string

	// DnsConfigFile 处理 polaris-sidecar 运行模式为 dns 的配置文件
	DnsConfigFile string

	// JavaAgentConfigFile 处理运行模式为 javaagent 的配置文件
	JavaAgentConfigFile string

	ValuesFile string

	// BootstrapConfigFile is the path to the mesh configuration file.
	BootstrapConfigFile string

	// CertFile is the path to the x509 certificate for https.
	CertFile string

	// KeyFile is the path to the x509 private key matching `CertFile`.
	KeyFile string
}

// env will be used for other things besides meshEnvoyConfig - when webhook is running in Istiod it can take advantage
// of the config and endpoint cache.
// nolint
func loadConfig(p TemplateFileConfig) (*InjectConfigInfo, error) {
	// 读取 mesh envoy模式的配置模板
	meshData, err := os.ReadFile(p.MeshConfigFile)
	if err != nil {
		return nil, err
	}
	var meshConf TemplateConfig
	if err := gyaml.Unmarshal(meshData, &meshConf); err != nil {
		log.InjectScope().Warnf("Failed to parse inject mesh config file %s", string(meshData))
		return nil, err
	}
	log.InjectScope().Infof("[MESH] New inject configuration: sha256sum %x", sha256.Sum256(meshData))
	log.InjectScope().Infof("[MESH] Policy: %v", meshConf.Policy)
	log.InjectScope().Infof("[MESH] AlwaysInjectSelector: %v", meshConf.AlwaysInjectSelector)
	log.InjectScope().Infof("[MESH] NeverInjectSelector: %v", meshConf.NeverInjectSelector)
	log.InjectScope().Infof("[MESH] InjectedAnnotations: %v", meshConf.InjectedAnnotations)
	log.InjectScope().Infof("[MESH] Template: |\n  %v", strings.Replace(meshConf.Template, "\n", "\n  ", -1))

	// 读取 dns 模式的配置模板
	dnsData, err := os.ReadFile(p.DnsConfigFile)
	if err != nil {
		return nil, err
	}
	var dnsConf TemplateConfig
	if err := gyaml.Unmarshal(dnsData, &dnsConf); err != nil {
		log.InjectScope().Warnf("Failed to parse inject dns config file %s", string(dnsData))
		return nil, err
	}
	log.InjectScope().Infof("[DNS] New inject configuration: sha256sum %x", sha256.Sum256(dnsData))
	log.InjectScope().Infof("[DNS] Policy: %v", dnsConf.Policy)
	log.InjectScope().Infof("[DNS] AlwaysInjectSelector: %v", dnsConf.AlwaysInjectSelector)
	log.InjectScope().Infof("[DNS] NeverInjectSelector: %v", dnsConf.NeverInjectSelector)
	log.InjectScope().Infof("[DNS] InjectedAnnotations: %v", dnsConf.InjectedAnnotations)
	log.InjectScope().Infof("[DNS] Template: |\n  %v", strings.Replace(dnsConf.Template, "\n", "\n  ", -1))

	// 读取 javaagent 模式的配置模板
	javaAgentData, err := os.ReadFile(p.JavaAgentConfigFile)
	if err != nil {
		return nil, err
	}
	var javaAgentConf TemplateConfig
	if err := gyaml.Unmarshal(javaAgentData, &javaAgentConf); err != nil {
		log.InjectScope().Warnf("Failed to parse inject java-agent config file %s", string(dnsData))
		return nil, err
	}
	log.InjectScope().Infof("[JavaAgent] New inject configuration: sha256sum %x", sha256.Sum256(javaAgentData))
	log.InjectScope().Infof("[JavaAgent] Policy: %v", javaAgentConf.Policy)
	log.InjectScope().Infof("[JavaAgent] AlwaysInjectSelector: %v", javaAgentConf.AlwaysInjectSelector)
	log.InjectScope().Infof("[JavaAgent] NeverInjectSelector: %v", javaAgentConf.NeverInjectSelector)
	log.InjectScope().Infof("[JavaAgent] InjectedAnnotations: %v", javaAgentConf.InjectedAnnotations)
	log.InjectScope().Infof("[JavaAgent] Template: |\n  %v", strings.Replace(javaAgentConf.Template, "\n", "\n  ", -1))

	// 读取 values
	valuesConfig, err := os.ReadFile(p.ValuesFile)
	if err != nil {
		return nil, err
	}
	log.InjectScope().Infof("[ValuesConfig] New inject configuration: sha256sum %x", sha256.Sum256(valuesConfig))

	// 读取 inject 配置
	meshEnvoyConfig, err := mesh.ReadMeshEnvoyConfig(p.BootstrapConfigFile)
	if err != nil {
		return nil, err
	}
	log.InjectScope().Infof("[MeshEnvoyConfig] New inject configuration: sha256sum %x", sha256.Sum256([]byte(
		meshEnvoyConfig.String())))

	// 读取 TLS 证书
	pair, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
	if err != nil {
		log.InjectScope().Errorf("Failed to load TLS certificate: %v", err)
		return nil, err
	}
	log.InjectScope().Infof("[X509KeyPair] New inject configuration: sha256sum %x", sha256.Sum256(javaAgentData))

	return &InjectConfigInfo{
		MeshInjectConf:      &meshConf,
		DnsInjectConf:       &dnsConf,
		JavaAgentInjectConf: &javaAgentConf,
		MeshEnvoyConf:       meshEnvoyConfig,
		ValuesConf:          string(valuesConfig),
		CertPair:            &pair,
	}, nil
}

func (p TemplateFileConfig) GetWatchList() []string {
	return []string{p.MeshConfigFile, p.DnsConfigFile, p.JavaAgentConfigFile, p.CertFile, p.KeyFile}
}
