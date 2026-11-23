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
	"strings"
	"testing"
)

const retryRBAC = `{"items":[{"kind":"ClusterRole","metadata":{"name":"owned","annotations":{"meta.helm.sh/release-name":"polaris-controller","meta.helm.sh/release-namespace":"release-ns"},"labels":{"app.kubernetes.io/instance":"polaris-controller","qcloud-app":"polaris-controller","app.kubernetes.io/managed-by":"Helm"}}}]}`

func TestResidualCleanupCanRetryAfterHelmReleaseDisappears(t *testing.T) {
	calls, _, err := runUninstall(t, []string{"polaris-controller", "release-ns"}, map[string]string{
		"RELEASE_JSON": validRelease, "RBAC_JSON": retryRBAC, "NAMESPACE_MISSING": "1", "CLEANUP_FAIL": "1",
	})
	if err == nil || !strings.Contains(calls, "helm uninstall") ||
		!strings.Contains(calls, "delete ClusterRole") {
		t.Fatalf("fixture did not interrupt cleanup: %s %v", calls, err)
	}
	retry, output, err := runUninstall(t, []string{"polaris-controller", "release-ns", "--cleanup-residual"}, map[string]string{
		"STATUS_FAIL": "1", "RELEASE_LIST_JSON": "[]", "RBAC_JSON": retryRBAC,
	})
	if err != nil ||
		!strings.Contains(retry, "--selector polarismesh.cn/sync-state=true,app.kubernetes.io/managed-by=polaris-controller") ||
		!strings.Contains(retry, "delete ClusterRole") ||
		!strings.Contains(retry, "--request-timeout=10s get namespace") ||
		!strings.Contains(retry, "--request-timeout=10s get pods") ||
		!strings.Contains(retry, "--request-timeout=10s get clusterrole,clusterrolebinding") ||
		strings.Contains(retry, "helm uninstall") {
		t.Fatalf("residual retry failed: calls=%s output=%s error=%v", retry, output, err)
	}
}

func TestResidualCleanupRefusesUnconfirmedAbsence(t *testing.T) {
	for _, tc := range []struct {
		name, namespace, list string
		fail                  bool
	}{
		{"missing namespace", "", "[]", false},
		{"release still exists", "release-ns", validReleaseList, false},
		{"query fails", "release-ns", "[]", true},
		{"malformed response", "release-ns", "{", false},
		{"missing name", "release-ns", `[{"namespace":"release-ns"}]`, false},
		{"invalid entry", "release-ns", `[null]`, false},
		{"wrong namespace", "release-ns", `[{"name":"other","namespace":"other-ns"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"RELEASE_LIST_JSON": tc.list, "RBAC_JSON": retryRBAC}
			if tc.fail {
				env["LIST_FAIL"] = "1"
			}
			calls, _, err := runUninstall(t, []string{"polaris-controller", tc.namespace, "--cleanup-residual"}, env)
			if err == nil || strings.Contains(calls, "kubectl") {
				t.Fatalf("unconfirmed cleanup proceeded: calls=%s error=%v", calls, err)
			}
		})
	}
}

func TestResidualCleanupRefusesLiveController(t *testing.T) {
	calls, output, err := runUninstall(t, []string{"polaris-controller", "release-ns", "--cleanup-residual"}, map[string]string{
		"STATUS_FAIL": "1", "RELEASE_LIST_JSON": "[]", "RBAC_JSON": retryRBAC, "WORKLOAD_PRESENT": "1",
	})
	if err == nil {
		t.Fatalf("live controller Pod did not block residual runtime cleanup: calls=%s output=%s", calls, output)
	}
	if !strings.Contains(calls, "get pods") || strings.Contains(calls, "delete configmap") ||
		strings.Contains(calls, "delete ClusterRole") {
		t.Fatalf("live controller cleanup was not guarded: calls=%s output=%s", calls, output)
	}
}

func TestResidualCleanupPreservesOtherReleaseAndManualResources(t *testing.T) {
	body := strings.ReplaceAll(retryRBAC, "release-ns", "other-ns")
	calls, output, err := runUninstall(t, []string{"polaris-controller", "release-ns", "--cleanup-residual"}, map[string]string{
		"RELEASE_LIST_JSON": "[]", "RBAC_JSON": body,
	})
	if err != nil || strings.Contains(calls, "kubectl delete") {
		t.Fatalf("other release was deleted: %s %s %v", calls, output, err)
	}
}
