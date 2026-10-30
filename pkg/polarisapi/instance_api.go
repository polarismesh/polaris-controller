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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
)

const (
	instanceQueryTimeout  = 10 * time.Second
	instanceQueryPageSize = 100
)

// GetInstances queries the console API instead of the SDK discovery cache.
func GetInstances(ctx context.Context, service *v1.Service) (instances []Instance, err error) {
	if service == nil || service.GetNamespace() == "" || service.GetName() == "" {
		return nil, fmt.Errorf("instance query requires a Service namespace and name")
	}
	ctx, cancel := context.WithTimeout(ctx, instanceQueryTimeout)
	defer cancel()
	started := time.Now()
	defer func() {
		status, code := "Success", "200000"
		if err != nil {
			status, code = "Failed", "500"
			log.SyncNamingScope().Errorf("Failed to query instances %s/%s: %v",
				service.GetNamespace(), service.GetName(), err)
		}
		metrics.InstanceRequestSync.WithLabelValues("Get", "Platform", status, code).
			Observe(time.Since(started).Seconds())
	}()

	query := url.Values{}
	query.Set("namespace", service.GetNamespace())
	query.Set("service", service.GetName())
	query.Set("limit", strconv.Itoa(instanceQueryPageSize))
	var amount uint32
	seen := make(map[string]struct{})
	for {
		query.Set("offset", strconv.Itoa(len(instances)))
		response, requestErr := getInstancesPage(ctx, service, query)
		if requestErr != nil {
			return nil, requestErr
		}
		if len(instances) == 0 {
			amount = *response.Amount
		} else if amount != *response.Amount {
			return nil, fmt.Errorf("instance count changed while querying %s/%s", service.GetNamespace(), service.GetName())
		}
		for _, instance := range response.Instances {
			if instance.Namespace != service.GetNamespace() || instance.Service != service.GetName() ||
				instance.Host == "" || instance.Port == nil || *instance.Port < 1 || *instance.Port > 65535 {
				return nil, fmt.Errorf("invalid instance in query for %s/%s", service.GetNamespace(), service.GetName())
			}
			key := instance.ID
			if key == "" {
				key = fmt.Sprintf("%s|%s|%d", instance.VpcID, instance.Host, *instance.Port)
			}
			if _, exists := seen[key]; exists {
				return nil, fmt.Errorf("duplicate instance %s while querying %s/%s", key, service.GetNamespace(), service.GetName())
			}
			seen[key] = struct{}{}
			instances = append(instances, instance)
		}
		if uint64(len(instances)) > uint64(amount) ||
			(len(response.Instances) == 0 && uint64(len(instances)) < uint64(amount)) {
			return nil, fmt.Errorf("incomplete instance query for %s/%s", service.GetNamespace(), service.GetName())
		}
		if uint64(len(instances)) == uint64(amount) {
			return instances, nil
		}
	}
}

func getInstancesPage(ctx context.Context, service *v1.Service, query url.Values) (GetInstancesResponse, error) {
	requestURL := fmt.Sprintf("%s%s?%s", PolarisHttpURL, addInstances, query.Encode())
	status, body, _, err := polarisHttpRequestWithContext(ctx, uuid.NewString(), http.MethodGet, requestURL, nil)
	if err != nil {
		return GetInstancesResponse{}, fmt.Errorf("query instances %s/%s: %w", service.GetNamespace(), service.GetName(), err)
	}
	var response GetInstancesResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode instance query %s/%s: %w", service.GetNamespace(), service.GetName(), err)
	}
	if status != http.StatusOK || response.Code != ExecuteSuccess {
		return response, fmt.Errorf("query instances %s/%s failed: HTTP=%d code=%d info=%s",
			service.GetNamespace(), service.GetName(), status, response.Code, response.Info)
	}
	if response.Amount == nil || response.Size == nil ||
		uint64(*response.Size) != uint64(len(response.Instances)) || len(response.Instances) > instanceQueryPageSize {
		return response, fmt.Errorf("incomplete instance query pagination for %s/%s",
			service.GetNamespace(), service.GetName())
	}
	return response, nil
}
