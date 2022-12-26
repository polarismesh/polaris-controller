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

package controller

import (
	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	v1 "k8s.io/api/core/v1"
)

// resyncWorker 定时对账
func (p *PolarisController) resyncWorker() {
	if !p.isPolarisServerHealthy.Load() {
		log.Info("Resync: Polaris server failed, not sync")
		return
	}

	p.resyncServiceCache.Range(func(key string, value *v1.Service) bool {
		v, ok := p.serviceCache.Load(util.GetOriginKeyWithResyncQueueKey(key))
		if !ok {
			p.enqueueService(key, value, "Add")
			return true
		}

		// 强制更新
		p.onServiceUpdate(v, value)
		return true
	})
}

// checkHealth 健康检查
func (p *PolarisController) checkHealth() {
	if polarisapi.CheckHealth() {
		// failed -> healthy, clear service cache and start full resync
		if !p.isPolarisServerHealthy.Load() {
			p.isPolarisServerHealthy.Store(true)
			p.serviceCache.Clear()
			log.Info("Polaris server health check: clear local cache and resync")
		}

		// 清除网络波动导致的健康检测失败记录
		p.polarisServerFailedTimes = 0
		return
	}

	// 失败三次以上认为server down
	if p.isPolarisServerHealthy.Load() {
		p.polarisServerFailedTimes++
		if p.polarisServerFailedTimes >= 3 {
			p.isPolarisServerHealthy.Store(false)
			p.polarisServerFailedTimes = 0
		}
	}
}
