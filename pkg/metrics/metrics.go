/**
 * Tencent is pleased to support the open source community by making polaris-go available.
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

package metrics

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const endpointSliceSubsystem = "polaris_controller"

var (
	// InstanceRequestSync 单次接口操作请求时间
	InstanceRequestSync = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      endpointSliceSubsystem,
			Name:           "sync_instance_pre_request_time",
			Help:           "单次接口操作实例请求时间",
			StabilityLevel: metrics.STABLE,
			Buckets:        metrics.ExponentialBuckets(0.001, 2, 16),
		},
		[]string{"operator", "type", "status", "code"},
	)

	// SyncTimes controller接收请求数
	SyncTimes = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      endpointSliceSubsystem,
			Name:           "sync_received_count",
			Help:           "平台接口对实例处理状态",
			StabilityLevel: metrics.STABLE,
		},
		[]string{"operator", "resource"},
	)

	// PolarisCount 统计集群中北极星数量
	PolarisCount = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      endpointSliceSubsystem,
			Name:           "polaris_count",
			Help:           "北极星数量",
			StabilityLevel: metrics.STABLE,
		},
		[]string{"service_namespace", "service_name", "polaris_namespace", "polaris_service"},
	)
)

var registerMetrics sync.Once

// RegisterMetrics registers EndpointSlice metrics.
func RegisterMetrics() {
	registerMetrics.Do(func() {
		legacyregistry.MustRegister(InstanceRequestSync)
		legacyregistry.MustRegister(SyncTimes)
		legacyregistry.MustRegister(PolarisCount)
	})
}
