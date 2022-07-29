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

package log

// logger type
const (
	// NamingLoggerName naming logger name, can use FindScope function to get the logger
	SyncLoggerName = "sync"
	// ConfigLoggerName config logger name, can use FindScope function to get the logger
	InjectLoggerName = "inject"
)

var (
	syncScope   = RegisterScope(SyncLoggerName, "sync logging messages.", 0)
	injectScope = RegisterScope(InjectLoggerName, "naming logging messages.", 0)
)

func allLoggerTypes() []string {
	return []string{SyncLoggerName, InjectLoggerName}
}

// DefaultScope default logging scope handler
func DefaultScope() *Scope {
	return defaultScope
}

// SyncScope naming logging scope handler
func SyncScope() *Scope {
	return syncScope
}

// InjectScope
func InjectScope() *Scope {
	return injectScope
}
