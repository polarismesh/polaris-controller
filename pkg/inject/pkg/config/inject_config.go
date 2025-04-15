package config

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"

	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
)

// InjectConfigInfo is a struct that contains all the configuration
type InjectConfigInfo struct {
	MeshInjectConf      *TemplateConfig
	DnsInjectConf       *TemplateConfig
	JavaAgentInjectConf *TemplateConfig
	MeshEnvoyConf       *mesh.MeshEnvoyConfig
	ValuesConf          string
	CertPair            *tls.Certificate
}

// helper function to generate a template version identifier from a
// hash of the un-executed template contents.
func sidecarTemplateVersionHash(in string) string {
	hash := sha256.Sum256([]byte(in))
	return hex.EncodeToString(hash[:])
}
