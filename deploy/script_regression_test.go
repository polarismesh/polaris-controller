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

package deploy_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const deployCommandTimeout = 60 * time.Second

func runDeploy(t *testing.T, args []string, env map[string]string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deployCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", append([]string{"deploy.sh"}, args...)...)
	command.Env = append(os.Environ(), "LOG_HOLD_SECONDS=0")
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestDeployRendersHelmValuesWithoutChangingTemplates(t *testing.T) {
	source := filepath.Join("kubernetes_v1.22", "helm", "values.yaml")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()
	output, err := runDeploy(t, []string{
		"render", "helm", "--kubernetes-version", "v1.22",
		"--output-dir", outputDir,
		"--set", "CONTROLLER_VERSION=cli-version",
		"--set", "POLARIS_TOKEN=cli-token",
	}, map[string]string{
		"PC_CLUSTER_NAME":  "env-cluster",
		"PC_POLARIS_TOKEN": "env-token",
	})
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}
	if strings.Contains(output, "cli-token") {
		t.Fatal("render output leaked POLARIS_TOKEN")
	}
	renderedPath := filepath.Join(outputDir, "v1.22", "values.yaml")
	rendered, err := os.ReadFile(renderedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"name: env-cluster",
		"tag: cli-version",
		`token: "cli-token"`,
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("rendered values are missing %q", want)
		}
	}
	for _, variable := range []string{
		"POLARIS_HOST", "CONTROLLER_VERSION", "SIDECAR_VERSION", "POLARIS_TOKEN",
		"POLARIS_OPERATOR", "ENVOY_VERSION", "CLUSTER_NAME", "JAVA_AGENT_INIT", "SYNC_MODE",
	} {
		if strings.Contains(string(rendered), "#"+variable+"#") {
			t.Errorf("rendered values still contain #%s#", variable)
		}
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("render modified the source values.yaml")
	}
	info, err := os.Stat(renderedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("rendered values permission=%o, want 600", info.Mode().Perm())
	}
}

func TestDeployManifestRenderIsRepeatable(t *testing.T) {
	outputDir := t.TempDir()
	args := []string{
		"render", "manifests", "--kubernetes-version", "v1.22",
		"--output-dir", outputDir,
		"--set", "POLARIS_HOST=polaris.example",
		"--set", "CLUSTER_NAME=test-cluster",
		"--set", "SYNC_MODE=all",
	}
	for i := 0; i < 2; i++ {
		output, err := runDeploy(t, args, nil)
		if err != nil {
			t.Fatalf("render %d failed: %v\n%s", i+1, err, output)
		}
	}
	configPath := filepath.Join(outputDir, "v1.22", "configmap.yaml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"serverAddress: polaris.example",
		`clusterName: "test-cluster"`,
		"mode: all",
	} {
		if !strings.Contains(string(config), want) {
			t.Errorf("rendered manifest is missing %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(outputDir, "v1.22", "secrets", "key.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "v1.22", "secrets", "secrets")); !os.IsNotExist(err) {
		t.Fatalf("repeat render created nested secrets directory: %v", err)
	}
}

func TestManifestUninstallPreservesNamespaceByDefault(t *testing.T) {
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	callsPath := filepath.Join(dir, "calls")
	mock := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CALLS"
`
	if err := os.WriteFile(kubectl, []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(extra ...string) string {
		t.Helper()
		if err := os.Remove(callsPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		args := []string{
			"uninstall", "manifests", "--kubernetes-version", "v1.22", "--dry-run",
		}
		args = append(args, extra...)
		output, err := runDeploy(t, args, map[string]string{"KUBECTL": kubectl, "CALLS": callsPath})
		if err != nil {
			t.Fatalf("uninstall failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("kubectl was not called: %v\n%s", err, output)
		}
		return string(data)
	}

	regular := run()
	webhook := strings.Index(regular, "delete mutatingwebhookconfiguration")
	injector := strings.Index(regular, "injector.yaml")
	controller := strings.Index(regular, "polaris-controller.yaml")
	if webhook < 0 || injector < 0 || controller < 0 || !(webhook < injector && injector < controller) {
		t.Fatalf("unsafe uninstall order: %s", regular)
	}
	for _, want := range []string{
		"delete secret polaris-sidecar-injector",
		"polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller",
	} {
		if !strings.Contains(regular, want) {
			t.Errorf("regular uninstall is missing %q: %s", want, regular)
		}
	}
	if strings.Contains(regular, "delete -f") && strings.Contains(regular, "namespace.yaml") {
		t.Fatalf("regular uninstall deleted the shared namespace: %s", regular)
	}

	optedIn := run("--delete-namespace")
	if !strings.Contains(optedIn, "namespace.yaml") {
		t.Fatalf("--delete-namespace did not opt in: %s", optedIn)
	}
}

func TestManifestUninstallWaitsForControllerPodsBeforeStateCleanup(t *testing.T) {
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	callsPath := filepath.Join(dir, "calls")
	mock := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CALLS"
if [[ " $* " == *" wait pods "* && "${WAIT_FAIL:-0}" == "1" ]]; then
  exit 1
fi
`
	if err := os.WriteFile(kubectl, []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}

	run := func(waitFail string) string {
		t.Helper()
		if err := os.Remove(callsPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		output, err := runDeploy(t, []string{
			"uninstall", "manifests", "--kubernetes-version", "v1.22",
		}, map[string]string{"KUBECTL": kubectl, "CALLS": callsPath, "WAIT_FAIL": waitFail})
		if err != nil {
			t.Fatalf("uninstall failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("kubectl was not called: %v\n%s", err, output)
		}
		return string(data)
	}

	regular := run("0")
	if !strings.Contains(regular, "wait pods --for=delete --selector k8s-app=polaris-controller --timeout=120s") {
		t.Fatalf("uninstall did not wait for controller Pods: %s", regular)
	}
	if strings.Contains(regular, "delete pods") {
		t.Fatalf("successful Pod wait still forced deletion: %s", regular)
	}
	stateIndex := strings.Index(regular, "delete configmap")
	waitIndex := strings.Index(regular, "wait pods")
	if stateIndex < 0 || waitIndex < 0 || waitIndex > stateIndex {
		t.Fatalf("state cleanup did not follow Pod wait: %s", regular)
	}

	timeout := run("1")
	if !strings.Contains(timeout, "wait pods") || !strings.Contains(timeout, "delete pods") {
		t.Fatalf("timed out Pod wait did not force deletion: %s", timeout)
	}
	forceIndex := strings.Index(timeout, "delete pods")
	stateIndex = strings.Index(timeout, "delete configmap")
	if forceIndex < 0 || stateIndex < 0 || forceIndex > stateIndex {
		t.Fatalf("state cleanup ran before forced Pod deletion: %s", timeout)
	}
	if !strings.Contains(timeout, "--force=true") ||
		!strings.Contains(timeout, "--grace-period=0") {
		t.Fatalf("forced Pod deletion is missing force flags: %s", timeout)
	}
}
