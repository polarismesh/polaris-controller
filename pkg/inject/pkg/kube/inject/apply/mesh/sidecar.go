/**
 * Tencent is pleased to support the open source community by making Polaris available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package mesh

import (
	"encoding/json"
	"strconv"

	"github.com/polarismesh/polaris-controller/common/log"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

const (
	EnvPolarisAddress                  = "POLARIS_ADDRESS"
	EnvSidecarBind                     = "SIDECAR_BIND"
	EnvSidecarPort                     = "SIDECAR_PORT"
	EnvSidecarNamespace                = "SIDECAR_NAMESPACE"
	EnvSidecarRecurseEnable            = "SIDECAR_RECURSE_ENABLE"
	EnvSidecarRecurseTimeout           = "SIDECAR_RECURSE_TIMEOUT"
	EnvSidecarRegion                   = "SIDECAR_REGION"
	EnvSidecarZone                     = "SIDECAR_ZONE"
	EnvSidecarCampus                   = "SIDECAR_CAMPUS"
	EnvSidecarNearbyMatchLevel         = "SIDECAR_NEARBY_MATCH_LEVEL"
	EnvSidecarLogRotateOutputPath      = "SIDECAR_LOG_ROTATE_OUTPUT_PATH"
	EnvSidecarLogErrorRotateOutputPath = "SIDECAR_LOG_ERROR_ROTATE_OUTPUT_PATH"
	EnvSidecarLogRotationMaxSize       = "SIDECAR_LOG_ROTATION_MAX_SIZE"
	EnvSidecarLogRotationMaxBackups    = "SIDECAR_LOG_ROTATION_MAX_BACKUPS"
	EnvSidecarLogRotationMaxAge        = "SIDECAR_LOG_ROTATION_MAX_AGE"
	EnvSidecarLogLevel                 = "SIDECAR_LOG_LEVEL"
	EnvSidecarDnsTtl                   = "SIDECAR_DNS_TTL"
	EnvSidecarDnsEnable                = "SIDECAR_DNS_ENABLE"
	EnvSidecarDnsSuffix                = "SIDECAR_DNS_SUFFIX"
	EnvSidecarDnsRouteLabels           = "SIDECAR_DNS_ROUTE_LABELS"
	EnvSidecarMeshTtl                  = "SIDECAR_MESH_TTL"
	EnvSidecarMeshEnable               = "SIDECAR_MESH_ENABLE"
	EnvSidecarMeshReloadInterval       = "SIDECAR_MESH_RELOAD_INTERVAL"
	EnvSidecarMeshAnswerIp             = "SIDECAR_MESH_ANSWER_IP"
	EnvSidecarMtlsEnable               = "SIDECAR_MTLS_ENABLE"
	EnvSidecarMtlsCAServer             = "SIDECAR_MTLS_CA_SERVER"
	EnvSidecarMetricEnable             = "SIDECAR_METRIC_ENABLE"
	EnvSidecarMetricListenPort         = "SIDECAR_METRIC_LISTEN_PORT"
	EnvSidecarRLSEnable                = "SIDECAR_RLS_ENABLE"

	ValueListenPort       = 15053
	ValueMetricListenPort = 15985
)

// SidecarConfig 定义了polaris-sidecar的配置
type SidecarConfig struct {
	Recurse    *RecurseConfig `json:"recurse"`
	Dns        *DnsConfig     `json:"dns"`
	Location   *Location      `json:"location"`
	LogOptions *LogOptions    `json:"log"`
}

// DnsConfig 定义了polaris-sidecar的dns配置
type DnsConfig struct {
	Suffix *string `json:"suffix"`
	TTL    *int    `json:"ttl"`
}

// RecurseConfig 定义了polaris-sidecar的递归配置
type RecurseConfig struct {
	Enabled *bool `json:"enabled"`
	Timeout *int  `json:"timeout"`
}

// Location 定义了polaris-sidecar的部署位置
type Location struct {
	Region     *string `json:"region"`
	Zone       *string `json:"zone"`
	Campus     *string `json:"campus"`
	MatchLevel *string `json:"match_level"`
}

type LogOptions struct {
	OutputLevel        *string `json:"output_level"`
	RotationMaxSize    *int    `json:"rotation_max_size"`
	RotationMaxAge     *int    `json:"rotation_max_age"`
	RotationMaxBackups *int    `json:"rotation_max_backups"`
}

func getSidecarConfig(data string) (*SidecarConfig, error) {
	config := SidecarConfig{}
	err := json.Unmarshal([]byte(data), &config)
	if err != nil {
		log.InjectScope().Errorf("getSidecarConfig failed: %v, raw:%s", err, data)
		return nil, err
	}
	log.InjectScope().Infof("getSidecarConfig: %s, raw:%s", utils.JsonString(config), data)
	return &config, nil
}

func fillEnv(envMap map[string]string, config *SidecarConfig, mode utils.SidecarMode) {
	// dns config
	if mode == utils.SidecarForDns && config.Dns != nil {
		if config.Dns.Suffix != nil {
			envMap[EnvSidecarDnsSuffix] = *config.Dns.Suffix
		}
		if config.Dns.TTL != nil && *config.Dns.TTL > 0 {
			envMap[EnvSidecarDnsTtl] = strconv.Itoa(*config.Dns.TTL)
		}
	}
	// recurse config
	if config.Recurse != nil {
		if config.Recurse.Enabled != nil && *config.Recurse.Enabled == false {
			envMap[EnvSidecarRecurseEnable] = strconv.FormatBool(*config.Recurse.Enabled)
		}
		if config.Recurse.Timeout != nil && *config.Recurse.Timeout > 0 {
			envMap[EnvSidecarRecurseTimeout] = strconv.Itoa(*config.Recurse.Timeout)
		}
	}
	// location
	if config.Location != nil {
		if config.Location.Region != nil {
			envMap[EnvSidecarRegion] = *config.Location.Region
		}
		if config.Location.Zone != nil {
			envMap[EnvSidecarZone] = *config.Location.Zone
		}
		if config.Location.Campus != nil {
			envMap[EnvSidecarCampus] = *config.Location.Campus
		}
		if config.Location.MatchLevel != nil {
			if _, ok := stringToMatchLevel[*config.Location.MatchLevel]; ok {
				envMap[EnvSidecarNearbyMatchLevel] = *config.Location.MatchLevel
			}
		}
	}
	// log
	if config.LogOptions != nil {
		if config.LogOptions.OutputLevel != nil && *config.LogOptions.OutputLevel != "" {
			if _, ok := stringToLevel[*config.LogOptions.OutputLevel]; ok {
				envMap[EnvSidecarLogLevel] = *config.LogOptions.OutputLevel
			}
		}
		if config.LogOptions.RotationMaxSize != nil && *config.LogOptions.RotationMaxSize > 0 {
			envMap[EnvSidecarLogRotationMaxSize] = strconv.Itoa(*config.LogOptions.RotationMaxSize)
		}
		if config.LogOptions.RotationMaxAge != nil && *config.LogOptions.RotationMaxAge > 0 {
			envMap[EnvSidecarLogRotationMaxAge] = strconv.Itoa(*config.LogOptions.RotationMaxAge)
		}
		if config.LogOptions.RotationMaxBackups != nil && *config.LogOptions.RotationMaxBackups > 0 {
			envMap[EnvSidecarLogRotationMaxBackups] = strconv.Itoa(*config.LogOptions.RotationMaxBackups)
		}
	}
}

var stringToMatchLevel = map[string]bool{
	"region": true,
	"zone":   true,
	"campus": true,
}

var stringToLevel = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
	"fatal": true,
}
