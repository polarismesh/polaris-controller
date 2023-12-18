// Tencent is pleased to support the open source community by making Polaris available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package polarisapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

const (
	addConfigFiles    = "/config/v1/configfiles"
	deleteConfigFiles = "/config/v1/configfiles"
	updateConfigFile  = "/config/v1/configfiles"
	releaseConfigFile = "/config/v1/configfiles/release"
)

func CreateConfigMap(configMap *v1.ConfigMap) (ConfigResponse, error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", configMap.GetNamespace(), configMap.GetName())

	url := fmt.Sprintf("%s%s", PolarisHttpURL, updateConfigFile)
	req := parseConfigFileRequest(configMap)
	resp, err := polarisConfigRequest(req, serviceMsg, url, http.MethodPost)
	if err != nil {
		return ConfigResponse{}, err
	}
	if err := releaseConfigMap(req); err != nil {
		return ConfigResponse{}, err
	}
	return resp, err
}

// UpdateConfigMap 平台更新配置文件接口
func UpdateConfigMap(configMap *v1.ConfigMap) (ConfigResponse, error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", configMap.GetNamespace(), configMap.GetName())

	url := fmt.Sprintf("%s%s", PolarisHttpURL, updateConfigFile)
	req := parseConfigFileRequest(configMap)
	resp, err := polarisConfigRequest(req, serviceMsg, url, http.MethodPut)
	if err != nil {
		return ConfigResponse{}, err
	}
	if err := releaseConfigMap(req); err != nil {
		return ConfigResponse{}, err
	}
	return resp, err
}

// DeleteConfigMap 平台删除配置文件接口
func DeleteConfigMap(configMap *v1.ConfigMap) error {
	key := fmt.Sprintf("[%s/%s]", configMap.GetNamespace(), configMap.GetName())
	req := parseConfigFileRequest(configMap)
	opdesc := "delete"
	log.SyncConfigScope().Infof("Start to %s ConfigMap [%s][%s]", opdesc, req.Namespace, req.Name)
	startTime := time.Now()
	defer func() {
		log.SyncConfigScope().Infof("Finish to %s %s (%v)", opdesc, key, time.Since(startTime))
	}()

	var response ConfigResponse
	requestByte, err := json.Marshal(req)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed to marsh request %s %v", key, err)
		return err
	}

	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s?namespace=%s&group=%s&name=%s", PolarisHttpURL, deleteConfigFiles,
		req.Namespace, req.Group, req.Name)
	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodDelete, url, requestByte)

	if err != nil {
		log.SyncConfigScope().Errorf("Failed to %s ConfigMap %s %v", opdesc, key, err)
		return err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			log.SyncConfigScope().Errorf("Failed to unmarshal result %s %s, %v, %s", opdesc, key, err, string(body))
			return err
		}
		if response.Code != uint32(ExistedResource) {
			log.SyncConfigScope().Error(fmt.Sprintf("Failed to %s ConfigMap %s", opdesc, key), zap.String("url", url),
				zap.Int32("code", int32(response.Code)), zap.String("info", response.Info))
			return fmt.Errorf("%s ConfigMap failed: "+response.Info, opdesc)
		}
	}

	return nil
}

// releaseConfigMap 平台删除配置文件接口
func releaseConfigMap(file *ConfigFile) error {
	opdesc := "release"
	key := fmt.Sprintf("[%s/%s]", file.Namespace, file.Name)
	req := &ConfigFileRelease{
		Name:      file.Name,
		Namespace: file.Namespace,
		Group:     file.Group,
		FileName:  file.Name,
		Content:   file.Content,
		Comment:   "release by kubernetes",
	}

	log.SyncConfigScope().Infof("Start to %s ConfigMap [%s][%s]", opdesc, req.Namespace, req.Name)
	startTime := time.Now()
	defer func() {
		log.SyncConfigScope().Infof("Finish to %s %s (%v)", opdesc, key, time.Since(startTime))
	}()

	var response ConfigResponse
	requestByte, err := json.Marshal(req)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed to marsh request %s %v", key, err)
		return err
	}

	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, releaseConfigFile)
	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		log.SyncConfigScope().Errorf("Failed to %s ConfigMap %s %v", opdesc, key, err)
		return err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			log.SyncConfigScope().Errorf("Failed to unmarshal result %s %s, %v, %s", opdesc, key, err, string(body))
			return err
		}
		if response.Code != uint32(ExistedResource) {
			log.SyncConfigScope().Error(fmt.Sprintf("Failed to %s ConfigMap %s", opdesc, key), zap.String("url", url),
				zap.Int32("code", int32(response.Code)), zap.String("info", response.Info))
			return fmt.Errorf("%s ConfigMap failed: "+response.Info, opdesc)
		}
	}

	return nil
}

var (
	configOperation = map[string]string{
		http.MethodPost: "create",
		http.MethodPut:  "update",
	}
)

func polarisConfigRequest(req *ConfigFile, key, url, method string) (ConfigResponse, error) {
	opdesc := configOperation[method]
	log.SyncConfigScope().Infof("Start to %s ConfigMap [%s][%s]", opdesc, req.Namespace, req.Name)
	startTime := time.Now()
	defer func() {
		log.SyncConfigScope().Infof("Finish to %s %s (%v)", opdesc, key, time.Since(startTime))
	}()

	var response ConfigResponse
	requestByte, err := json.Marshal(req)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed to marsh request %s %v", key, err)
		return response, err
	}

	requestID := uuid.New().String()
	statusCode, body, _, err := polarisHttpRequest(requestID, method, url, requestByte)

	if err != nil {
		log.SyncConfigScope().Errorf("Failed to %s ConfigMap %s %v %q %v", opdesc, key, url, string(requestByte), err)
		return response, err
	}

	if statusCode != http.StatusOK {
		if err = json.Unmarshal(body, &response); err != nil {
			log.SyncConfigScope().Errorf("Failed to unmarshal result %s %s, %v, %s", opdesc, key, err, string(body))
			return ConfigResponse{}, err
		}
		if response.Code != uint32(ExistedResource) {
			log.SyncConfigScope().Error(fmt.Sprintf("Failed to %s ConfigMap %s", opdesc, key), zap.String("url", url),
				zap.String("req-body", string(requestByte)), zap.Int32("code", int32(response.Code)),
				zap.String("info", response.Info))
			return response, fmt.Errorf("%s ConfigMap failed: "+response.Info, opdesc)
		}
	}

	return response, nil
}

func parseConfigFileRequest(configMap *v1.ConfigMap) *ConfigFile {
	content, _ := yaml.Marshal(configMap.Data)
	createRequest := ConfigFile{
		Name:      configMap.Name,
		Namespace: configMap.Namespace,
		Content:   string(content),
		Tags:      []*ConfigFileTag{},
	}

	existSourceKey := false
	for k, v := range configMap.Annotations {
		if k == util.PolarisConfigEncrypt && v == "true" {
			createRequest.Encrypted = true
			continue
		}
		if k == util.PolarisConfigEncryptAlog {
			createRequest.EncryptAlgo = v
			continue
		}
		if k == util.PolarisConfigGroup {
			createRequest.Group = v
			continue
		}
		if k == util.InternalConfigFileSyncSourceKey {
			existSourceKey = true
		}
		createRequest.Tags = append(createRequest.Tags, &ConfigFileTag{
			Key:   k,
			Value: v,
		})
	}
	for k, v := range configMap.Labels {
		createRequest.Tags = append(createRequest.Tags, &ConfigFileTag{
			Key:   k,
			Value: v,
		})
	}
	if !existSourceKey {
		createRequest.Tags = append(createRequest.Tags, &ConfigFileTag{
			Key:   util.InternalConfigFileSyncSourceKey,
			Value: util.SourceFromKubernetes,
		})
	}
	return &createRequest
}
