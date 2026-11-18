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
	"os"
	"sync"
	"time"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap/zapcore"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	logBytes = metrics.NewCounterVec(&metrics.CounterOpts{
		Name: "polaris_controller_log_written_bytes_total", Help: "Bytes written to each log sink",
		StabilityLevel: metrics.STABLE,
	}, []string{"sink"})
	logSuppressed = metrics.NewCounterVec(&metrics.CounterOpts{
		Name: "polaris_controller_log_suppressed_failures_total", Help: "Repeated failure logs suppressed",
		StabilityLevel: metrics.STABLE,
	}, []string{"scope"})
	logRotations = metrics.NewCounterVec(&metrics.CounterOpts{
		Name: "polaris_controller_log_rotations_total", Help: "Successful size based log rotations",
		StabilityLevel: metrics.STABLE,
	}, []string{"sink"})
	logRotationInterval = metrics.NewHistogramVec(&metrics.HistogramOpts{
		Name: "polaris_controller_log_rotation_interval_seconds", Help: "Time between size based log rotations",
		StabilityLevel: metrics.STABLE, Buckets: metrics.ExponentialBuckets(1, 2, 18),
	}, []string{"sink"})
	registerLogMetrics sync.Once
)

func registerLoggingMetrics() {
	registerLogMetrics.Do(func() {
		legacyregistry.MustRegister(logBytes, logSuppressed, logRotations, logRotationInterval)
	})
}

type countedLogSink struct {
	zapcore.WriteSyncer
	kind string
}

func (s *countedLogSink) Write(p []byte) (int, error) {
	n, err := s.WriteSyncer.Write(p)
	logBytes.WithLabelValues(s.kind).Add(float64(n))
	return n, err
}

// Lumberjack rotates on size + len(p) > MaxSize. Track successful writes under
// the same lock and initialize from the existing file so restarts are counted.
type rotatingLogWriter struct {
	logger       *lumberjack.Logger
	kind         string
	mu           sync.Mutex
	initialized  bool
	size         int64
	lastRotation time.Time
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.initialized {
		if info, err := os.Stat(w.logger.Filename); err == nil {
			w.size = info.Size()
		}
		w.lastRotation = time.Now()
		w.initialized = true
	}
	maxSize := w.logger.MaxSize
	if maxSize == 0 {
		maxSize = defaultRotationMaxSize
	}
	rotating := w.size+int64(len(p)) > int64(maxSize)*1024*1024
	n, err := w.logger.Write(p)
	logBytes.WithLabelValues(w.kind).Add(float64(n))
	if err == nil && rotating {
		now := time.Now()
		logRotations.WithLabelValues(w.kind).Inc()
		logRotationInterval.WithLabelValues(w.kind).Observe(now.Sub(w.lastRotation).Seconds())
		w.lastRotation = now
		w.size = 0
	}
	w.size += int64(n)
	return n, err
}
