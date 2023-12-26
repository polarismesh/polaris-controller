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

// logger type
const (
	// ConfigLoggerName config logger name, can use FindScope function to get the logger
	InjectLoggerName = "inject"
	// SyncNamingLoggerName naming sync logger name, can use FindScope function to get the logger
	SyncNamingLoggerName = "syncnaming"
	// SyncConfigLoggerName config sync logger name, can use FindScope function to get the logger
	SyncConfigLoggerName = "syncconfig"
	// SyncConfigLoggerName config map sync logger name, can use FindScope function to get the logger
	SyncConfigMapLoggerName = "synccm"
	// TraceLoggerName trace logger name, can use FindScope function to get the logger
	TraceLoggerName = "trace"
)

var (
	injectScope     = RegisterScope(InjectLoggerName, "pod inject logging messages.", 0)
	syncNamingScope = RegisterScope(SyncNamingLoggerName, "naming sync logging messages.", 0)
	syncConfigScope = RegisterScope(SyncConfigLoggerName, "config sync logging messages.", 0)
	syncCmScope     = RegisterScope(SyncConfigMapLoggerName, "configmap sync logging messages.", 0)
	traceScope      = RegisterScope(TraceLoggerName, "trace logging messages.", 0)
)

func allLoggerTypes() []string {
	return []string{SyncNamingLoggerName, SyncConfigLoggerName,
		SyncConfigMapLoggerName, InjectLoggerName, DefaultLoggerName}
}

// DefaultScope default logging scope handler
func DefaultScope() *Scope {
	return defaultScope
}

// SyncNamingScope naming logging scope handler
func SyncNamingScope() *Scope {
	return syncNamingScope
}

// SyncConfigScope naming logging scope handler
func SyncConfigScope() *Scope {
	return syncConfigScope
}

// SyncConfigMapScope naming logging scope handler
func SyncConfigMapScope() *Scope {
	return syncCmScope
}

// InjectScope
func InjectScope() *Scope {
	return injectScope
}

// TraceScope
func TraceScope() *Scope {
	return traceScope
}
