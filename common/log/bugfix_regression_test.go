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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap/zapcore"
	"k8s.io/component-base/metrics/legacyregistry"
)

func TestFailureLimiterFirstChangeIntervalAndExhaustion(t *testing.T) {
	limiter := failureLogLimiter{states: make(map[string]failureLogState)}
	now := time.Unix(100, 0)
	if emit, _ := limiter.allow("ns/svc", "401000", now, false); !emit {
		t.Fatal("first failure was hidden")
	}
	for i := 0; i < 99; i++ {
		if emit, _ := limiter.allow("ns/svc", "401000", now, false); emit {
			t.Fatal("repeated failure was not limited")
		}
	}
	if emit, suppressed := limiter.allow("ns/svc", "400208", now, false); !emit || suppressed != 99 {
		t.Fatalf("state change hidden: emit=%v suppressed=%d", emit, suppressed)
	}
	if emit, _ := limiter.allow("ns/svc", "400208", now, true); !emit {
		t.Fatal("retry exhaustion hidden")
	}
	if emit, _ := limiter.allow("ns/svc", "400208", now.Add(time.Minute), false); !emit {
		t.Fatal("periodic summary hidden")
	}
	if emit, _ := limiter.allow("ns/other", "400208", now, false); !emit {
		t.Fatal("one service suppressed another")
	}
}

func TestFailureLimiterHasBoundedMemory(t *testing.T) {
	limiter := failureLogLimiter{states: make(map[string]failureLogState)}
	for i := 0; i < failureLogCapacity+100; i++ {
		limiter.allow(string(rune(i)), "error", time.Unix(int64(i), 0), false)
	}
	if len(limiter.states) != failureLogCapacity {
		t.Fatalf("failure states grew to %d", len(limiter.states))
	}
}

type failingSyncer struct{ err error }

func (s failingSyncer) Write(p []byte) (int, error) { return len(p), nil }
func (s failingSyncer) Sync() error                 { return s.err }

func TestStdoutPipeSyncFiltersOnlyUnsupportedFsync(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original }()
	options := *DefaultOptions()[DefaultLoggerName]
	options.OutputPaths = []string{"stdout"}
	options.RotateOutputPath = ""
	cores, _, _, err := prepZap(&options)
	if err != nil {
		t.Fatal(err)
	}
	for _, core := range cores {
		if err := core.Sync(); err != nil {
			t.Fatalf("normal pipe sync failed: %v", err)
		}
	}
	file, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, stream := range []*os.File{writer, file} {
		syncer := &standardStreamSyncer{WriteSyncer: failingSyncer{err: syscall.EIO}, stream: stream}
		if !errors.Is(syncer.Sync(), syscall.EIO) {
			t.Fatal("real disk error hidden")
		}
	}
	syncer := &standardStreamSyncer{WriteSyncer: failingSyncer{err: syscall.EINVAL}, stream: file}
	if !errors.Is(syncer.Sync(), syscall.EINVAL) {
		t.Fatal("regular file fsync failure was hidden")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	closed := &standardStreamSyncer{WriteSyncer: zapcore.AddSync(writer), stream: writer}
	if closed.Sync() == nil {
		t.Fatal("closed stdout error was hidden")
	}
}

func TestLogBytesAndRotationsRemainObservable(t *testing.T) {
	registerLoggingMetrics()
	path := filepath.Join(t.TempDir(), "rotation.log")
	writer := &rotatingLogWriter{logger: &lumberjack.Logger{Filename: path, MaxSize: 1, MaxBackups: 2}, kind: "test_rotate"}
	defer writer.logger.Close()
	data := []byte(strings.Repeat("x", 600*1024))
	for i := 0; i < 2; i++ {
		if n, err := writer.Write(data); err != nil || n != len(data) {
			t.Fatalf("write=%d error=%v", n, err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "rotation-*.log"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("rotation backups=%v error=%v", backups, err)
	}
	families, err := legacyregistry.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	foundBytes, foundRotation := false, false
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "sink" && label.GetValue() == "test_rotate" {
					if family.GetName() == "polaris_controller_log_written_bytes_total" {
						foundBytes = metric.GetCounter().GetValue() == float64(len(data)*2)
					}
					if family.GetName() == "polaris_controller_log_rotations_total" {
						foundRotation = metric.GetCounter().GetValue() == 1
					}
				}
			}
		}
	}
	if !foundBytes || !foundRotation {
		t.Fatalf("missing metrics: bytes=%v rotation=%v", foundBytes, foundRotation)
	}
}
