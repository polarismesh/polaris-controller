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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

var (
	PolarisHttpURL     = "http://127.0.0.1:8090"
	PolarisGrpc        = "127.0.0.1:8091"
	PolarisConfigGrpc  = "127.0.0.1:8093"
	PolarisAccessToken = ""
	PolarisOperator    = ""
)

const (
	addInstances       = "/naming/v1/instances"
	deleteInstances    = "/naming/v1/instances/delete"
	getService         = "/naming/v1/services"
	updateService      = "/naming/v1/services"
	createService      = "/naming/v1/services"
	createServiceAlias = "/naming/v1/service/alias"
	updateServiceAlias = "/naming/v1/service/alias"
	getServiceAliases  = "/naming/v1/service/aliases"
	getNamespace       = "/naming/v1/namespaces"
	createNamespace    = "/naming/v1/namespaces"
	checkHealth        = ""
	getUserToken       = "/core/v1/user/token"
)

// AddInstances 平台增加实例接口
func AddInstances(instances []Instance, size int, msg string) (err error) {

	log.SyncNamingScope().Infof("Start to add all %s", msg)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to add all %s (%v)", msg, time.Since(startTime))
	}()

	// 从这里开始拆分
	instanceArray := splitArray(instances, size)

	url := fmt.Sprintf("%s%s", PolarisHttpURL, addInstances)

	var wg sync.WaitGroup
	page := len(instanceArray)
	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)

			if err != nil {
				log.SyncNamingScope().Errorf("Failed to marsh request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}

			_, body, times, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)
			if err != nil {
				log.SyncNamingScope().Errorf("Failed request %s [%d/%d], err %v. (%s)", msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}

			var response AddResponse
			err = json.Unmarshal(body, &response)
			log.SyncNamingScope().Debugf("Send add %s [%d/%d], body is %s. (%s)", msg, i+1, page, string(requestByte), requestID)

			if err != nil {
				log.SyncNamingScope().Errorf("Failed unmarshal result %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.",
						msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}

			metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", addInstanceRequestStatus(response),
				fmt.Sprint(response.Code)).
				Observe(times.Seconds())

			dealAddInstanceResponse(response, msg, i, page, requestID, polarisErrors)

		}(i, v)
	}
	wg.Wait()

	return polarisErrors.GetError()
}

func dealAddInstanceResponse(response AddResponse, msg string,
	i int, page int, requestID string, polarisErrors *PErrors) {
	if len(response.Responses) == 0 {
		switch response.Code {
		case 200000:
			log.SyncNamingScope().Infof("Success add all %s [%d/%d], info %s. (%s)",
				msg, i+1, page, response.Info, requestID)
			return
		case 401000:
			log.SyncNamingScope().Infof("Failed add all %s [%d/%d], info %s. (%s)",
				msg, i+1, page, response.Info, requestID)
			return
		case ExistedResource, SameInstanceRequest:
			log.SyncNamingScope().Infof(
				"skip add all %s [%d/%d], existing instance response (%d), treat as existed. (%s)",
				msg, i+1, page, response.Code, requestID)
			return
		}
		log.SyncNamingScope().Infof("Failed add %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
		return
	}

	for _, ins := range response.Responses {
		if ins.Code == SameInstanceRequest {
			log.SyncNamingScope().Infof(
				"skip add %s [%s:%d] [%d/%d], same instance request (400208), treat as existed. (%s)",
				msg, ins.Instance.Host, ins.Instance.Port, i+1, page, requestID)
			continue
		}

		// 400201 已存在，200000 成功，401000 保持历史忽略行为。
		if ins.Code != ExistedResource && ins.Code != 200000 && ins.Code != 401000 {
			log.SyncNamingScope().Errorf("Failed add %s [%s:%d] [%d/%d], info %s. (%s)",
				msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
			polarisErrors.Append(PError{
				IP:   ins.Instance.Host,
				Port: ins.Instance.Port,
				Code: util.Uint32Ptr(ins.Code),
				Info: ins.Info,
			})
		} else {
			log.SyncNamingScope().Debugf("Success add %s [%s:%d] [%d/%d], info %s. (%s)",
				msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
		}
	}
}

func addInstanceRequestStatus(response AddResponse) string {
	if len(response.Responses) == 0 {
		switch response.Code {
		case ExistedResource, SameInstanceRequest:
			return "Idempotent"
		case 200000, 401000:
			return "Success"
		default:
			return "Failed"
		}
	}

	hasIdempotent := false
	for _, ins := range response.Responses {
		switch ins.Code {
		case SameInstanceRequest, ExistedResource:
			hasIdempotent = true
		case 200000, 401000:
		default:
			return "Failed"
		}
	}
	if hasIdempotent {
		return "Idempotent"
	}
	if len(response.Responses) != 0 {
		return "Success"
	}
	return "Failed"
}

// DeleteInstances 平台删除实例接口
func DeleteInstances(instances []Instance, size int, msg string) (err error) {
	// 从这里开始拆分
	instanceArray := splitArray(instances, size)

	url := fmt.Sprintf("%s%s", PolarisHttpURL, deleteInstances)
	// 查看并发请求过程中，是否有失败的情况，只要存在，就应该放入队列中，重新同步
	var wg sync.WaitGroup
	page := len(instanceArray)
	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)

			log.SyncNamingScope().Debugf("Send delete %s [%d/%d], body is %s. (%s)",
				msg, i+1, page, string(requestByte), requestID)
			if err != nil {
				log.Errorf("Failed to marsh request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}
			var response AddResponse
			statusCode, body, times, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

			if err != nil {
				log.SyncNamingScope().Errorf("Failed to request %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").
					Observe(times.Seconds())
				return
			}
			if len(bytes.TrimSpace(body)) == 0 && statusCode == http.StatusOK {
				log.SyncNamingScope().Infof("Success delete all %s [%d/%d]. (%s)",
					msg, i+1, page, requestID)
				metrics.InstanceRequestSync.WithLabelValues("Delete", "Platform", "Success", "200000").
					Observe(times.Seconds())
				return
			}
			err = json.Unmarshal(body, &response)
			log.SyncNamingScope().Debugf("%s [%d/%d], body is %s. (%s)",
				msg, i+1, page, string(body), requestID)

			if err != nil {
				log.SyncNamingScope().Errorf("Failed unmarshal result %s [%d/%d], err %v. (%s)",
					msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				metrics.InstanceRequestSync.WithLabelValues("Add", "Platform", "Failed", "500").Observe(times.Seconds())
				return
			}
			dealDeleteInstanceResponse(response, msg, i, page, requestID, times, polarisErrors)
		}(i, v)
	}
	wg.Wait()
	return polarisErrors.GetError()
}

func dealDeleteInstanceResponse(response AddResponse, msg string, i int, page int, requestID string,
	times time.Duration, polarisErrors *PErrors) {
	if len(response.Responses) != 0 {
		if response.Code != 200000 {
			code := response.Code
			if code == 0 {
				code = 500
			}
			log.SyncNamingScope().Errorf("Failed delete %s top-level [%d/%d], info %s. (%s)",
				msg, i+1, page, response.Info, requestID)
			polarisErrors.Append(PError{
				Code: util.Uint32Ptr(code),
				Info: response.Info,
			})
		}
		for _, ins := range response.Responses {
			if ins.Code != 200000 {
				log.SyncNamingScope().Errorf("Failed delete %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
				polarisErrors.Append(PError{
					IP:   ins.Instance.Host,
					Port: ins.Instance.Port,
					Code: util.Uint32Ptr(ins.Code),
					Info: ins.Info,
				})
			} else {
				log.SyncNamingScope().Debugf("Success delete %s [%s:%d] [%d/%d], info %s. (%s)",
					msg, ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info, requestID)
			}
		}
		metrics.InstanceRequestSync.WithLabelValues(
			"Delete", "Platform", "PerInstance", fmt.Sprint(response.Code)).Observe(times.Seconds())
		return
	}

	switch response.Code {
	case 200000:
		log.SyncNamingScope().Infof("Success delete all %s [%d/%d], info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		metrics.InstanceRequestSync.WithLabelValues(
			"Delete", "Platform", "Success", fmt.Sprint(response.Code)).Observe(times.Seconds())
		return
	case 401000:
		log.SyncNamingScope().Errorf("Failed delete all %s [%d/%d], unauthorized, info %s. (%s)",
			msg, i+1, page, response.Info, requestID)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(response.Code),
			Info: response.Info,
		})
		metrics.InstanceRequestSync.WithLabelValues(
			"Delete", "Platform", "Failed", fmt.Sprint(response.Code)).Observe(times.Seconds())
		return
	default:
		log.SyncNamingScope().Infof("Failed delete %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
	}
	metrics.InstanceRequestSync.WithLabelValues(
		"Delete", "Platform", "Failed", fmt.Sprint(response.Code)).Observe(times.Seconds())
}

// UpdateInstances 修改平台实例接口
func UpdateInstances(instances []Instance, size int, msg string) (err error) {

	log.SyncNamingScope().Infof("Start to add all %s", msg)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to add all %s (%v)", msg, time.Since(startTime))
	}()

	// 从这里开始拆分
	instanceArray := splitArray(instances, size)
	url := fmt.Sprintf("%s%s", PolarisHttpURL, addInstances)

	var wg sync.WaitGroup
	page := len(instanceArray)

	polarisErrors := NewPErrors()
	for i, v := range instanceArray {
		wg.Add(1)
		go func(i int, v []Instance) {
			defer wg.Done()
			requestID := uuid.New().String()
			requestByte, err := json.Marshal(v)
			if err != nil {
				log.SyncNamingScope().Errorf("Failed to marsh request %s] [%d/%d], err %v.",
					msg, i+1, page, err)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg,
						i+1, page, err),
				})
				return
			}
			log.SyncNamingScope().Debugf("Update msg body is %s", string(requestByte))
			httpCode, body, _, err := polarisHttpRequest(requestID, http.MethodPut, url, requestByte)
			if err != nil {
				log.SyncNamingScope().Errorf("Failed request %s [%d/%d], err %v. requestId: %s", msg, i+1, page, err, requestID)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.", msg, i+1, page, err),
				})
				return
			}

			if httpCode == http.StatusOK {
				return
			}

			var response AddResponse
			err = json.Unmarshal(body, &response)
			if err != nil {
				log.SyncNamingScope().Errorf("Failed unmarshal result %s [%d/%d], err %v.",
					msg, i+1, page, err)
				polarisErrors.Append(PError{
					Code: util.Uint32Ptr(500),
					Info: fmt.Sprintf("Failed to marsh request %s [%d/%d], err %v.",
						msg, i+1, page, err),
				})
				return
			}
			log.SyncNamingScope().Errorf("update response is %v", response)
			dealUpdateInstanceResponse(response, msg, i, page, polarisErrors)
		}(i, v)
	}
	wg.Wait()
	return polarisErrors.GetError()
}

func dealUpdateInstanceResponse(response AddResponse, msg string, i int, page int, polarisErrors *PErrors) {
	// 添加成功或者权限错误，都跳过
	if response.Code == 200000 {
		log.SyncNamingScope().Infof("Success add all %s [%d/%d], info %s.", msg, i+1, page, response.Info)
		return
	}

	if len(response.Responses) != 0 {
		for _, ins := range response.Responses {
			// 200002 update data is no change, no need to update
			// 200000 execute success
			if ins.Code != 200002 && ins.Code != 200000 {
				log.SyncNamingScope().Errorf("Failed add %s [%s/%s] [%s:%d] [%d/%d], info %s.",
					msg, ins.Instance.Namespace, ins.Instance.Service,
					ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info)
				polarisErrors.Append(PError{
					IP:   ins.Instance.Host,
					Port: ins.Instance.Port,
					Code: util.Uint32Ptr(ins.Code),
					Info: ins.Info,
				})
			} else {
				log.SyncNamingScope().Debugf("Success add %s [%s/%s] [%s:%d] [%d/%d], info %s.",
					msg, ins.Instance.Namespace, ins.Instance.Service,
					ins.Instance.Host, ins.Instance.Port, i+1, page, ins.Info)
			}
		}
	} else {
		log.SyncNamingScope().Infof("Failed update %s all [%d/%d], info %s.", msg, i+1, page, response.Info)
		polarisErrors.Append(PError{
			Code: util.Uint32Ptr(500),
			Info: response.Info,
		})
	}
}

// GetService 查询服务接口
// GET /naming/v1/services?参数名=参数值
func GetService(service *v1.Service) (res GetServiceResponse, err error) {
	polarisName, polarisNamespace := getPolarisName(service)
	serviceMsg := fmt.Sprintf("[%s/%s]", polarisNamespace, polarisName)

	log.SyncNamingScope().Infof("Start to get %s", serviceMsg)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to get %s (%v)", serviceMsg, time.Since(startTime))
	}()
	var response GetServiceResponse
	requestID := uuid.New().String()

	if polarisNamespace == "" {
		return response, fmt.Errorf("failed service is invalid, polarisNamespace is empty")
	}
	if polarisName == "" {
		return response, fmt.Errorf("failed service is invalid, polarisService is empty")
	}

	url := fmt.Sprintf("%s%s?namespace=%s&name=%s&offset=%d&limit=%d",
		PolarisHttpURL, getService, polarisNamespace, polarisName, 0, 100)

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodGet, url, nil)

	log.SyncNamingScope().Debugf("Get service %s, body %s", serviceMsg, string(body))

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to get request %s %v", serviceMsg, err)
		return response, err
	}

	err = json.Unmarshal(body, &response)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to unmarshal result %s %v", serviceMsg, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		log.SyncNamingScope().Errorf("Failed to get service %s %s", serviceMsg, response.Info)
		return response, fmt.Errorf("failed to get service %s %s", serviceMsg, response.Info)
	}

	return response, nil
}

// ListService 查询服务列表
// GET /naming/v1/services?keys=platform&values=tke
func ListService(clusterID string) (res GetServiceResponse, err error) {

	log.SyncNamingScope().Info("Start to get platform service list")
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to get platform service list (%v)", time.Since(startTime))
	}()
	var response GetServiceResponse
	requestID := uuid.New().String()

	url := fmt.Sprintf("%s%s?keys=platform&values=tke&offset=%d&limit=%d",
		PolarisHttpURL, getService, 0, 100)

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodGet, url, nil)

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to get platform service list %v", err)
		return response, err
	}

	err = json.Unmarshal(body, &response)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to unmarshal platform service list %v", err)
		return response, err
	}

	if statusCode != http.StatusOK {
		log.SyncNamingScope().Errorf("Failed to get platform service list %s", response.Info)
		return response, fmt.Errorf("failed to get platform service list %s", response.Info)
	}

	return response, nil
}

func getPolarisPorts(service *v1.Service) string {
	var ports []string
	for _, port := range service.Spec.Ports {
		ports = append(ports, strconv.Itoa(int(port.Port)))
	}

	return strings.Join(ports, ",")
}

// CreateService 创建北极星服务
func CreateService(service *v1.Service) (CreateServicesResponse, error) {
	polarisName, polarisNamespace := getPolarisName(service)
	serviceMsg := fmt.Sprintf("[%s/%s]", polarisNamespace, polarisName)

	log.SyncNamingScope().Infof("Start to create service [%s][%s]", polarisNamespace, polarisName)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to update %s (%v)", serviceMsg, time.Since(startTime))
	}()

	var response CreateServicesResponse
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createService)

	createRequest := []CreateServiceRequest{
		{
			Name:      polarisName,
			Namespace: polarisNamespace,
			Owners:    Source,
			Ports:     getPolarisPorts(service),
			Metadata:  service.Labels,
		},
	}

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to marsh request %s %v", serviceMsg, err)
		return response, err
	}

	log.SyncNamingScope().Debugf("create service %s, body %s", serviceMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to create service %s %v", serviceMsg, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			log.SyncNamingScope().Errorf("Failed to unmarshal result %s, %v, %s", serviceMsg, err, string(body))
			return CreateServicesResponse{}, err
		}
		if response.Code != ExistedResource {
			log.SyncNamingScope().Errorf("Failed to create service %s %v", serviceMsg, response.Info)
			return response, fmt.Errorf("create namespace failed: %s", response.Info)
		}
	}

	return response, nil
}

// CreateServiceAlias 创建北极星服务别名
func CreateServiceAlias(service *v1.Service, owners string) (CreateServiceAliasResponse, error) {
	var response CreateServiceAliasResponse
	alias := service.GetAnnotations()[util.PolarisAliasService]
	aliasNs := service.GetAnnotations()[util.PolarisAliasNamespace]
	if alias == "" || aliasNs == "" {
		return response, fmt.Errorf("service alias namespace and name must both be set")
	}

	polarisName, polarisNamespace := getPolarisName(service)

	serviceAliasMsg := fmt.Sprintf("[%s/%s], [%s/%s]", polarisNamespace, polarisName, aliasNs, alias)

	log.SyncNamingScope().Infof("Start to create service alias %s", serviceAliasMsg)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to create service alias %s (%v)", serviceAliasMsg, time.Since(startTime))
	}()

	createNsResponse, err := CreateNamespaces(aliasNs)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed create namespaces in CreateServiceAlias %s, err %s, resp %v",
			serviceAliasMsg, err, createNsResponse)

		return response, err
	}

	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createServiceAlias)

	createRequest := newServiceAliasRequest(service, owners, "", "")

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to marsh request %s %v", serviceAliasMsg, err)
		return response, err
	}

	log.SyncNamingScope().Debugf("create service alias %s, body %s", serviceAliasMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to create service alias %s %v", serviceAliasMsg, err)
		return response, err
	}

	if err = json.Unmarshal(body, &response); err != nil {
		log.SyncNamingScope().Errorf("Failed to unmarshal result %s, %v, %s", serviceAliasMsg, err, string(body))
		return CreateServiceAliasResponse{}, err
	}
	if statusCode != http.StatusOK || response.Code != ExecuteSuccess {
		log.SyncNamingScope().Errorf("Failed to create service alias %s, code %d, info %s",
			serviceAliasMsg, response.Code, response.Info)
		return response, fmt.Errorf("create service alias failed, code=%d, info=%s", response.Code, response.Info)
	}

	return response, nil
}

// GetServiceAlias 查询指定 alias 的当前绑定。
func GetServiceAlias(aliasNamespace, alias string) (Alias, bool, error) {
	var response GetServiceAliasesResponse
	if aliasNamespace == "" || alias == "" {
		return Alias{}, false, fmt.Errorf("alias namespace and name must both be set")
	}

	requestID := uuid.New().String()
	query := url.Values{}
	query.Set("alias", alias)
	query.Set("alias_namespace", aliasNamespace)
	query.Set("offset", "0")
	query.Set("limit", "10")
	requestURL := fmt.Sprintf("%s%s?%s", PolarisHttpURL, getServiceAliases, query.Encode())

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodGet, requestURL, nil)
	if err != nil {
		return Alias{}, false, err
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return Alias{}, false, fmt.Errorf("unmarshal service alias response: %w", err)
	}
	if statusCode != http.StatusOK || response.Code != ExecuteSuccess {
		return Alias{}, false, fmt.Errorf("get service alias %s/%s failed, code=%d, info=%s",
			aliasNamespace, alias, response.Code, response.Info)
	}

	for _, item := range response.Aliases {
		if item.Alias == alias && item.AliasNamespace == aliasNamespace {
			return item, true, nil
		}
	}
	return Alias{}, false, nil
}

// UpdateServiceAlias 更新已有 alias 指向的源服务。
func UpdateServiceAlias(service *v1.Service, current Alias, owners string) (CreateServiceAliasResponse, error) {
	var response CreateServiceAliasResponse
	polarisName, polarisNamespace := getPolarisName(service)
	alias := service.GetAnnotations()[util.PolarisAliasService]
	aliasNs := service.GetAnnotations()[util.PolarisAliasNamespace]
	serviceAliasMsg := fmt.Sprintf("[%s/%s], [%s/%s]", polarisNamespace, polarisName, aliasNs, alias)

	updateRequest := newServiceAliasRequest(service, owners, current.ServiceToken, current.Comment)
	requestByte, err := json.Marshal(updateRequest)
	if err != nil {
		return response, err
	}

	requestID := uuid.New().String()
	requestURL := fmt.Sprintf("%s%s", PolarisHttpURL, updateServiceAlias)
	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPut, requestURL, requestByte)
	if err != nil {
		return response, err
	}
	if err = json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("unmarshal update service alias response: %w", err)
	}
	if statusCode != http.StatusOK || (response.Code != ExecuteSuccess && response.Code != NoNeedUpdate) {
		return response, fmt.Errorf("update service alias %s failed, code=%d, info=%s",
			serviceAliasMsg, response.Code, response.Info)
	}

	return response, nil
}

func newServiceAliasRequest(service *v1.Service, owners, serviceToken, comment string) *CreateServiceAliasRequest {
	polarisName, polarisNamespace := getPolarisName(service)
	return &CreateServiceAliasRequest{
		Service:        polarisName,
		Namespace:      polarisNamespace,
		Owners:         owners,
		Alias:          service.GetAnnotations()[util.PolarisAliasService],
		AliasNamespace: service.GetAnnotations()[util.PolarisAliasNamespace],
		ServiceToken:   serviceToken,
		Comment:        comment,
	}
}

// UpdateService 更新服务字段
// PUT /naming/v1/services
func UpdateService(service *v1.Service, request []Service) (int, PutServicesResponse, error) {
	polarisName, polarisNamespace := getPolarisName(service)
	serviceMsg := fmt.Sprintf("[%s/%s]", polarisNamespace, polarisName)
	log.SyncNamingScope().Infof("Start to update %s", serviceMsg)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to update %s (%v)", serviceMsg, time.Since(startTime))
	}()

	var response PutServicesResponse
	requestID := uuid.New().String()

	url := fmt.Sprintf("%s%s", PolarisHttpURL, updateService)

	requestByte, err := json.Marshal(request)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to marsh request %s %v", serviceMsg, err)
		return 0, PutServicesResponse{}, err
	}

	log.SyncNamingScope().Debugf("Put service %s, body %s", serviceMsg, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPut, url, requestByte)

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to get request %s %v", serviceMsg, err)
		return statusCode, PutServicesResponse{}, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			log.SyncNamingScope().Errorf("Failed to unmarshal result %s, %v, %s", serviceMsg, err, string(body))
			return statusCode, PutServicesResponse{}, err
		}
		log.SyncNamingScope().Errorf("Failed to update result %s %v", serviceMsg, response.Info)
		return statusCode, response, fmt.Errorf("Put service failed: %s ", response.Info)
	}

	return statusCode, response, nil
}

// CreateNamespaces 创建北极星命名空间
func CreateNamespaces(namespace string) (CreateNamespacesResponse, error) {
	log.SyncNamingScope().Infof("Start to create namespace %s", namespace)
	startTime := time.Now()
	defer func() {
		log.SyncNamingScope().Infof("Finish to create namespace %s (%v)", namespace, time.Since(startTime))
	}()

	var response CreateNamespacesResponse
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, createNamespace)

	createRequest := []CreateNamespacesRequest{
		{
			Name:   namespace,
			Owners: Source,
		},
	}

	requestByte, err := json.Marshal(createRequest)
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to marsh request %s %v", namespace, err)
		return response, err
	}

	log.SyncNamingScope().Debugf("create namespace %s, body %s", namespace, string(requestByte))

	statusCode, body, _, err := polarisHttpRequest(requestID, http.MethodPost, url, requestByte)

	if err != nil {
		log.SyncNamingScope().Errorf("Failed to get result %s %v", namespace, err)
		return response, err
	}

	if statusCode != http.StatusOK {
		err = json.Unmarshal(body, &response)
		if err != nil {
			log.SyncNamingScope().Errorf("Failed to unmarshal result %s, %v, %s", namespace, err, string(body))
			return CreateNamespacesResponse{}, err
		}
		if response.Responses == nil || len(response.Responses) == 0 ||
			response.Responses[0].Code != ExistedResource {
			log.SyncNamingScope().Errorf("Failed to create namespace %s ,error response: %v", namespace, response)
			return response, fmt.Errorf("create namespace failed: %s", response.Info)
		}
	}

	return response, nil
}

// CheckHealth checks whether the polaris server is healthy
func CheckHealth() bool {
	requestID := uuid.New().String()
	url := fmt.Sprintf("%s%s", PolarisHttpURL, checkHealth)

	statusCode, _, _, err := polarisHttpRequest(requestID, http.MethodGet, url, nil)

	if err != nil || statusCode != http.StatusOK {
		log.SyncNamingScope().Debug("Failed to check health of polaris server")
		return false
	}

	return true
}

func splitArray(instances []Instance, size int) [][]Instance {

	size64 := float64(size)
	length := len(instances)
	page64 := math.Ceil(float64(length) / size64)
	page := int(page64)

	var instanceArray [][]Instance

	for i := 0; i < page; i++ {
		start := i * size
		end := start + size
		if i == page-1 {
			end = length
		}
		instanceArray = append(instanceArray, instances[start:end])
	}

	return instanceArray
}

// polarisHttpRequest
func polarisHttpRequest(requestID string, method string, url string, requestByte []byte) (int, []byte, time.Duration,
	error) {
	startTime := time.Now()
	req, err := http.NewRequest(method, url, bytes.NewReader(requestByte))
	if err != nil {
		log.Errorf("Failed to set request %v", err)
		return 0, nil, 0, err
	}
	accessToken, err := lookAccessToken()
	if err != nil {
		log.Errorf("Failed to lookup access token %v", err)
		return 0, nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Request-Id", requestID)
	req.Header.Set(AccessTokenHeader, accessToken)

	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		log.Errorf("Failed to get request %v", err)
		return 0, nil, time.Since(startTime), err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		log.Errorf("Failed to get request %v", err)
		return 0, nil, time.Since(startTime), err
	}

	return resp.StatusCode, body, time.Since(startTime), err
}

func lookAccessToken() (string, error) {
	if len(PolarisOperator) == 0 {
		return PolarisAccessToken, nil
	}
	url := fmt.Sprintf("%s%s?id=%s", PolarisHttpURL, getUserToken, PolarisOperator)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Errorf("Failed to set request %v", err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Request-Id", uuid.NewString())
	req.Header.Set(AccessTokenHeader, PolarisAccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Errorf("Failed to get request %v", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("Failed to get request %v", err)
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(string(body))
	}

	userResp := &Response{}
	if err := json.Unmarshal(body, userResp); err != nil {
		return "", err
	}

	return userResp.User.AuthToken, nil
}

// getPolarisName, 解析北极星的Name
func getPolarisName(svr *v1.Service) (string, string) {
	return svr.GetName(), svr.GetNamespace()
}
