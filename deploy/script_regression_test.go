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
	for _, secret := range []string{"cli-token", "env-token"} {
		if strings.Contains(output, secret) {
			t.Fatalf("render output leaked POLARIS_TOKEN value %q", secret)
		}
	}
	for _, want := range []string{
		"[polaris-controller][VARIABLE] POLARIS_HOST=polaris.polaris-system",
		"[polaris-controller][VARIABLE] CONTROLLER_VERSION=cli-version",
		"[polaris-controller][VARIABLE] CLUSTER_NAME=env-cluster",
		"[polaris-controller][VARIABLE] POLARIS_TOKEN=********",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("render output is missing %q", want)
		}
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

func TestDeployNormalizesKubernetesVersions(t *testing.T) {
	for _, test := range []struct {
		version string
		want    string
	}{
		{version: "v1.21.14", want: "v1.21"},
		{version: "v1.22", want: "v1.22"},
		{version: "1.23", want: "v1.22"},
		{version: "v1.28.5+k3s1", want: "v1.22"},
	} {
		t.Run(test.version, func(t *testing.T) {
			outputDir := t.TempDir()
			output, err := runDeploy(t, []string{
				"render", "manifests",
				"--kubernetes-version", test.version,
				"--output-dir", outputDir,
				"--set", "POLARIS_HOST=polaris.example",
				"--set", "CLUSTER_NAME=test-cluster",
			}, nil)
			if err != nil {
				t.Fatalf("render failed: %v\n%s", err, output)
			}
			entries, err := os.ReadDir(outputDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != test.want {
				t.Fatalf("manifest family=%v, want %s", entries, test.want)
			}
			if _, err := os.Stat(filepath.Join(outputDir, test.want, "configmap.yaml")); err != nil {
				t.Fatalf("rendered manifest missing: %v", err)
			}
		})
	}

	output, err := runDeploy(t, []string{
		"render", "manifests", "--kubernetes-version", "v1.20.15",
		"--output-dir", t.TempDir(),
	}, nil)
	if err == nil || !strings.Contains(output, "unsupported Kubernetes manifest version") {
		t.Fatalf("Kubernetes v1.20 was accepted: %v\n%s", err, output)
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
if [[ " $* " == *" delete pods "* && "${FORCE_DELETE_FAIL:-0}" == "1" ]]; then
  exit 1
fi
`
	if err := os.WriteFile(kubectl, []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}

	run := func(extra map[string]string) string {
		t.Helper()
		env := map[string]string{"KUBECTL": kubectl, "CALLS": callsPath}
		for key, value := range extra {
			env[key] = value
		}
		if err := os.Remove(callsPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		output, err := runDeploy(t, []string{
			"uninstall", "manifests", "--kubernetes-version", "v1.22",
		}, env)
		if err != nil {
			t.Fatalf("uninstall failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("kubectl was not called: %v\n%s", err, output)
		}
		return string(data)
	}

	regular := run(map[string]string{"WAIT_FAIL": "0"})
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

	timeout := run(map[string]string{"WAIT_FAIL": "1"})
	if !strings.Contains(timeout, "wait pods") || !strings.Contains(timeout, "delete pods") {
		t.Fatalf("timed out Pod wait did not force deletion: %s", timeout)
	}
	forceIndex := strings.Index(timeout, "delete pods")
	stateIndex = strings.Index(timeout, "delete configmap")
	if forceIndex < 0 || stateIndex < 0 || forceIndex > stateIndex {
		t.Fatalf("state cleanup ran before forced Pod deletion: %s", timeout)
	}
	if !strings.Contains(timeout, "--force=true") ||
		!strings.Contains(timeout, "--grace-period=0") ||
		!strings.Contains(timeout, "--wait=false") ||
		!strings.Contains(timeout, "--request-timeout=10s") {
		t.Fatalf("forced Pod deletion is missing force flags: %s", timeout)
	}

	forceFailed := run(map[string]string{
		"WAIT_FAIL": "1", "FORCE_DELETE_FAIL": "1",
	})
	if !strings.Contains(forceFailed, "delete configmap") {
		t.Fatalf("failed forced Pod deletion blocked state cleanup: %s", forceFailed)
	}
}

func TestManifestUninstallUsesBoundedApiRequestsBeforePodWait(t *testing.T) {
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	callsPath := filepath.Join(dir, "calls")
	mock := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CALLS"
if [[ " $* " == *" delete "* && "${PRE_DELETE_FAIL:-0}" == "1" ]]; then
  exit 1
fi
`
	if err := os.WriteFile(kubectl, []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}

	run := func(preDeleteFail string) string {
		t.Helper()
		if err := os.Remove(callsPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		output, err := runDeploy(t, []string{
			"uninstall", "manifests", "--kubernetes-version", "v1.22",
		}, map[string]string{
			"KUBECTL": kubectl, "CALLS": callsPath, "PRE_DELETE_FAIL": preDeleteFail,
		})
		if preDeleteFail == "1" {
			if err == nil {
				t.Fatalf("pre-delete API failure was ignored: %s", output)
			}
		} else if err != nil {
			t.Fatalf("uninstall failed: %v\n%s", err, output)
		}
		data, err := os.ReadFile(callsPath)
		if err != nil {
			t.Fatalf("kubectl was not called: %v\n%s", err, output)
		}
		return string(data)
	}

	regular := run("0")
	for _, line := range strings.Split(regular, "\n") {
		if !strings.Contains(line, " delete ") {
			continue
		}
		if !strings.Contains(line, "--request-timeout=10s") ||
			!strings.Contains(line, "--wait=false") ||
			strings.Contains(line, "--wait=true") {
			t.Fatalf("unbounded resource delete: %s", line)
		}
	}
	if !strings.Contains(regular, "wait pods --for=delete --selector k8s-app=polaris-controller --timeout=120s") {
		t.Fatalf("uninstall did not keep the single 120s controller Pod wait: %s", regular)
	}

	failed := run("1")
	if strings.Contains(failed, "delete configmap") {
		t.Fatalf("pre-delete API failure still reached state cleanup: %s", failed)
	}
}

func TestManifestUninstallPreDeleteCannotBlockOnFinalizer(t *testing.T) {
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	callsPath := filepath.Join(dir, "calls")
	mock := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CALLS"
if [[ " $* " == *" delete "* && "$*" != *"--wait=false"* ]]; then
  sleep 30
fi
`
	if err := os.WriteFile(kubectl, []byte(mock), 0700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "deploy.sh",
		"uninstall", "manifests", "--kubernetes-version", "v1.22")
	command.Env = append(os.Environ(),
		"LOG_HOLD_SECONDS=0", "KUBECTL="+kubectl, "CALLS="+callsPath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pre-delete finalizer blocked uninstall: %v\n%s", err, output)
	}
	if _, readErr := os.ReadFile(callsPath); readErr != nil {
		t.Fatalf("kubectl was not called: %v\n%s", readErr, output)
	}
}

func TestManifestUninstallLogsDeleteSuccessAndTimeout(t *testing.T) {
	dir := t.TempDir()
	successKubectl := filepath.Join(dir, "kubectl-success")
	if err := os.WriteFile(successKubectl, []byte("#!/usr/bin/env bash\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}

	successOutput, err := runDeploy(t, []string{
		"uninstall", "manifests", "--kubernetes-version", "v1.22",
	}, map[string]string{"KUBECTL": successKubectl})
	if err != nil {
		t.Fatalf("successful uninstall failed: %v\n%s", err, successOutput)
	}
	for _, want := range []string{
		"[SUCCESS] mutatingwebhookconfiguration polaris-sidecar-injector: delete request accepted",
		"[SUCCESS] controller Pods: deleted within 120s",
		"[SUCCESS] sync-state ConfigMaps: delete request accepted",
	} {
		if !strings.Contains(successOutput, want) {
			t.Fatalf("success log is missing %q:\n%s", want, successOutput)
		}
	}

	timeoutKubectl := filepath.Join(dir, "kubectl-timeout")
	timeoutMock := `#!/usr/bin/env bash
set -euo pipefail
if [[ " $* " == *" wait pods "* ]]; then
  echo "error: timed out waiting for the condition" >&2
  exit 1
fi
if [[ " $* " == *" delete pods "* ]]; then
  echo "error: context deadline exceeded" >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(timeoutKubectl, []byte(timeoutMock), 0700); err != nil {
		t.Fatal(err)
	}
	timeoutOutput, err := runDeploy(t, []string{
		"uninstall", "manifests", "--kubernetes-version", "v1.22",
	}, map[string]string{"KUBECTL": timeoutKubectl})
	if err != nil {
		t.Fatalf("timed-out controller Pods blocked cleanup: %v\n%s", err, timeoutOutput)
	}
	for _, want := range []string{
		"[WARN] controller Pods: deletion wait timed out after 120s; forcing deletion",
		"[WARN] controller Pods (force delete): delete request timed out",
		"[WARN] controller Pods: continuing cleanup after force-delete request did not complete",
		"[SUCCESS] sync-state ConfigMaps: delete request accepted",
	} {
		if !strings.Contains(timeoutOutput, want) {
			t.Fatalf("timeout log is missing %q:\n%s", want, timeoutOutput)
		}
	}
}
