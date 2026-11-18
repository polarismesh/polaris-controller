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
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package log

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerWritesStdoutAndRotateFiles(t *testing.T) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
		_ = reader.Close()
		_ = writer.Close()
	}()

	dir := t.TempDir()
	normalPath := filepath.Join(dir, "polaris.log")
	errorPath := filepath.Join(dir, "polaris-error.log")
	scope := RegisterScope("logoutputtest", "logger output test", 0)
	if err := Configure(map[string]*Options{
		scope.Name(): {
			OutputPaths:           []string{"stdout"},
			ErrorOutputPaths:      []string{"stderr"},
			RotateOutputPath:      normalPath,
			ErrorRotateOutputPath: errorPath,
			JSONEncoding:          true,
			OutputLevel:           "info",
			StackTraceLevel:       "none",
		},
	}); err != nil {
		t.Fatal(err)
	}

	scope.Infof("write operation succeeded")
	scope.Errorf("write operation failed")
	// stdout is a pipe in this test; some platforms reject Sync on pipes.
	// File writes are still flushed before this call returns.
	_ = Sync()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout

	stdout, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	normal, err := os.ReadFile(normalPath)
	if err != nil {
		t.Fatal(err)
	}
	errorOnly, err := os.ReadFile(errorPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(stdout), "write operation succeeded") ||
		!strings.Contains(string(stdout), "write operation failed") {
		t.Fatalf("stdout missing log entries: %s", stdout)
	}
	if !strings.Contains(string(normal), "write operation succeeded") ||
		!strings.Contains(string(normal), "write operation failed") {
		t.Fatalf("normal log missing entries: %s", normal)
	}
	if strings.Contains(string(errorOnly), "write operation succeeded") ||
		!strings.Contains(string(errorOnly), "write operation failed") {
		t.Fatalf("error log has wrong entries: %s", errorOnly)
	}
}
