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

package log

import (
	"sync"
	"time"
)

const (
	failureLogInterval = time.Minute
	failureLogCapacity = 1024
)

type failureLogState struct {
	signature  string
	lastLogged time.Time
	lastSeen   time.Time
	suppressed int
}

type failureLogLimiter struct {
	mu     sync.Mutex
	states map[string]failureLogState
}

var syncFailures = failureLogLimiter{states: make(map[string]failureLogState)}

func (l *failureLogLimiter) allow(key, signature string, now time.Time, force bool) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, exists := l.states[key]
	if exists && state.signature == signature && now.Sub(state.lastLogged) < failureLogInterval && !force {
		state.suppressed++
		state.lastSeen = now
		l.states[key] = state
		return false, 0
	}
	if !exists && len(l.states) >= failureLogCapacity {
		oldestKey := ""
		var oldest time.Time
		for candidate, item := range l.states {
			if oldestKey == "" || item.lastSeen.Before(oldest) {
				oldestKey, oldest = candidate, item.lastSeen
			}
		}
		delete(l.states, oldestKey)
	}
	l.states[key] = failureLogState{signature: signature, lastLogged: now, lastSeen: now}
	return true, state.suppressed
}

// ReportFailure logs the first failure, changes in error class, periodic
// summaries, and retry exhaustion. The original error remains intact.
func (s *Scope) ReportFailure(key, signature string, failure error, force, warning bool) bool {
	if failure == nil || signature == "cancelled" {
		return false
	}
	allowed, suppressed := syncFailures.allow(s.name+"|"+key, signature, time.Now(), force)
	if !allowed {
		logSuppressed.WithLabelValues(s.name).Inc()
		return false
	}
	if warning {
		s.Warnf("Sync operation %s failed: code=%s suppressed=%d exhausted=%t error=%v",
			key, signature, suppressed, force, failure)
	} else {
		s.Errorf("Sync operation %s failed: code=%s suppressed=%d exhausted=%t error=%v",
			key, signature, suppressed, force, failure)
	}
	return true
}

func (s *Scope) ClearFailure(key string) {
	syncFailures.mu.Lock()
	state, exists := syncFailures.states[s.name+"|"+key]
	delete(syncFailures.states, s.name+"|"+key)
	syncFailures.mu.Unlock()
	if exists {
		s.Infof("Sync operation %s recovered: code=%s suppressed=%d", key, state.signature, state.suppressed)
	}
}
