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
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mockHelm = `#!/usr/bin/env bash
set -euo pipefail
printf 'helm %s\n' "$*" >> "$CALLS"
if [[ "$1" == "status" ]]; then
  if [[ "${STATUS_FAIL:-0}" == "1" ]]; then
    echo 'release not found or access denied' >&2
    exit 1
  fi
  printf '%s\n' "$RELEASE_JSON"
elif [[ "$1" == "list" ]]; then
  if [[ "${LIST_FAIL:-0}" == "1" ]]; then
    exit 1
  fi
  printf '%s\n' "$RELEASE_LIST_JSON"
elif [[ "$1" == "uninstall" && "${UNINSTALL_FAIL:-0}" == "1" ]]; then
  exit 1
fi
`

const mockKubectl = `#!/usr/bin/env bash
set -euo pipefail
printf 'kubectl %s\n' "$*" >> "$CALLS"
if [[ "$1" == "get" && "$2" == "clusterrole,clusterrolebinding" ]]; then
  printf '%s\n' "$RBAC_JSON"
fi
`

func runUninstall(t *testing.T, args []string, env map[string]string) (string, string, error) {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq is required for uninstall script regression tests")
	}
	dir := t.TempDir()
	helm, kubectl, calls := filepath.Join(dir, "helm"), filepath.Join(dir, "kubectl"), filepath.Join(dir, "calls")
	for path, content := range map[string]string{helm: mockHelm, kubectl: mockKubectl} {
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", append([]string{"uninstall_helm.sh"}, args...)...)
	command.Env = append(os.Environ(), "HELM="+helm, "KUBECTL="+kubectl, "JQ="+jq, "CALLS="+calls,
		"RELEASE_LIST_JSON="+validReleaseList)
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	callBytes, readErr := os.ReadFile(calls)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return string(callBytes), string(output), err
}

const validRelease = `{"name":"polaris-controller","namespace":"release-ns","version":1,"chart":null,"info":{"status":"deployed"}}`
const validReleaseList = `[{"name":"polaris-controller","namespace":"release-ns","revision":"1","chart":"polaris-controller-1.3.0","status":"deployed"}]`

func TestUninstallStopsBeforeCleanupForUnconfirmedRelease(t *testing.T) {
	for _, test := range []struct {
		name, namespace, release, list string
		statusFail, listFail           bool
	}{
		{name: "status failure", namespace: "release-ns", release: validRelease, statusFail: true},
		{name: "wrong namespace", namespace: "other-ns", release: validRelease},
		{name: "wrong release", namespace: "release-ns",
			release: `{"name":"other","namespace":"release-ns","chart":{"metadata":{"name":"polaris-controller"}}}`},
		{name: "other chart", namespace: "release-ns", release: validRelease,
			list: `[{"name":"polaris-controller","namespace":"release-ns","revision":"1","chart":"other-chart-1.3.0","status":"deployed"}]`},
		{name: "similar chart name", namespace: "release-ns", release: validRelease,
			list: `[{"name":"polaris-controller","namespace":"release-ns","revision":"1","chart":"polaris-controller-other-1.3.0","status":"deployed"}]`},
		{name: "chart lookup failure", release: validRelease, listFail: true},
		{name: "chart lookup empty", release: validRelease, list: `[]`},
		{name: "revision changes", release: validRelease,
			list: `[{"name":"polaris-controller","namespace":"release-ns","revision":"2","chart":"polaris-controller-1.3.0","status":"deployed"}]`},
		{name: "missing namespace", release: `{"name":"polaris-controller","chart":{"metadata":{"name":"polaris-controller"}}}`},
		{name: "malformed status", release: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{"RELEASE_JSON": test.release, "RBAC_JSON": `{"items":[]}`}
			if test.statusFail {
				env["STATUS_FAIL"] = "1"
			}
			if test.listFail {
				env["LIST_FAIL"] = "1"
			}
			if test.list != "" {
				env["RELEASE_LIST_JSON"] = test.list
			}
			calls, output, err := runUninstall(t, []string{"polaris-controller", test.namespace}, env)
			if err == nil || strings.Contains(calls, "helm uninstall") || strings.Contains(calls, "kubectl") {
				t.Fatalf("unconfirmed release reached cleanup: calls=%s output=%s error=%v", calls, output, err)
			}
		})
	}
}

func TestUninstallStopsCleanupWhenHelmUninstallFails(t *testing.T) {
	calls, output, err := runUninstall(t, []string{"polaris-controller", "release-ns"}, map[string]string{
		"RELEASE_JSON": validRelease, "UNINSTALL_FAIL": "1", "RBAC_JSON": `{"items":[]}`,
	})
	if err == nil || strings.Contains(calls, "kubectl") {
		t.Fatalf("failed uninstall reached cleanup: calls=%s output=%s error=%v", calls, output, err)
	}
}

func residualRBAC(kind, name, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"kind": kind,
		"metadata": map[string]interface{}{
			"name": name,
			"labels": map[string]string{
				"app.kubernetes.io/instance": "polaris-controller", "qcloud-app": "polaris-controller",
				"app.kubernetes.io/managed-by": "Helm",
			},
			"annotations": map[string]string{
				"meta.helm.sh/release-name": "polaris-controller", "meta.helm.sh/release-namespace": namespace,
			},
		},
	}
}

func TestUninstallOnlyDeletesConfirmedReleaseResidualRBAC(t *testing.T) {
	role := residualRBAC("ClusterRole", "owned-role", "release-ns")
	binding := residualRBAC("ClusterRoleBinding", "owned-binding", "release-ns")
	otherNamespace := residualRBAC("ClusterRole", "other-namespace", "other-ns")
	otherChart := residualRBAC("ClusterRole", "other-chart", "release-ns")
	otherChart["metadata"].(map[string]interface{})["labels"].(map[string]string)["qcloud-app"] = "other-chart"
	otherRelease := residualRBAC("ClusterRole", "other-release", "release-ns")
	otherRelease["metadata"].(map[string]interface{})["annotations"].(map[string]string)["meta.helm.sh/release-name"] = "other"
	unmanaged := residualRBAC("ClusterRole", "unmanaged", "release-ns")
	delete(unmanaged["metadata"].(map[string]interface{}), "annotations")
	body, err := json.Marshal(map[string]interface{}{"items": []interface{}{
		role, binding, otherNamespace, otherChart, otherRelease, unmanaged,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, namespaceProvided := range []bool{false, true} {
		t.Run(map[bool]string{false: "resolve namespace from status", true: "explicit namespace"}[namespaceProvided],
			func(t *testing.T) {
				args := []string{"polaris-controller"}
				if namespaceProvided {
					args = append(args, "release-ns")
				}
				calls, output, err := runUninstall(t, args, map[string]string{
					"RELEASE_JSON": validRelease, "RBAC_JSON": string(body), "CONTROLLER_NAMESPACE": "polaris-system",
				})
				if err != nil {
					t.Fatalf("normal uninstall failed: calls=%s output=%s error=%v", calls, output, err)
				}
				for _, expected := range []string{
					"helm list --all --namespace release-ns --filter ^polaris-controller$ --output json",
					"helm uninstall polaris-controller --namespace release-ns --wait",
					"kubectl delete ClusterRole --ignore-not-found=true --wait=true -- owned-role",
					"kubectl delete ClusterRoleBinding --ignore-not-found=true --wait=true -- owned-binding",
					"--selector polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller",
				} {
					if !strings.Contains(calls, expected) {
						t.Fatalf("missing scoped cleanup %q in calls=%s", expected, calls)
					}
				}
				for _, forbidden := range []string{"kubectl delete clusterrole,clusterrolebinding", "other-namespace",
					"other-chart", "other-release", "unmanaged"} {
					if strings.Contains(calls, forbidden) {
						t.Fatalf("uninstall deleted unrelated RBAC %q: %s", forbidden, calls)
					}
				}
			})
	}
}
