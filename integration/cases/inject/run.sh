#!/usr/bin/env bash
# Tencent is pleased to support the open source community by making Polaris available.
#
# Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
#
# Licensed under the BSD 3-Clause License (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# https://opensource.org/licenses/BSD-3-Clause
#
# Unless required by applicable law or agreed to in writing, software distributed
# under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
# CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../lib.sh"

polaris_init

inject_namespaces_absent() {
  all_ns_absent \
    "${NS_INJECT}" "${NS_INJECT_POD_LABEL}" "${NS_INJECT_JAVA}" "${NS_INJECT_LEGACY}"
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting injection namespaces"
  cleanup_case_namespaces
  wait_for "injection namespace deletion" "${WAIT_TIMEOUT}" inject_namespaces_absent
fi

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap cleanup_case_namespaces EXIT
fi

log "apply injection manifests"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"

pod_json() {
  kube -n "$1" get pod "$2" -o json
}

pod_exists() {
  pod_json "$1" "$2" >/dev/null 2>&1
}

java_workload_pod_exists() {
  [[ -n "$(kube -n "${NS_INJECT_JAVA}" get pod -l app=java-workload -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)" ]]
}

for item in \
  "${NS_INJECT}/dns-ok" \
  "${NS_INJECT}/dns-disabled-annotation" \
  "${NS_INJECT}/dns-disabled-label" \
  "${NS_INJECT}/dns-hostnetwork" \
  "${NS_INJECT_POD_LABEL}/pod-label-injected" \
  "${NS_INJECT_JAVA}/java-config" \
  "${NS_INJECT_JAVA}/java-hostnetwork" \
  "${NS_INJECT_LEGACY}/legacy-label-injected"; do
  namespace="${item%%/*}"
  pod="${item#*/}"
  wait_for "pod ${item}" "${WAIT_TIMEOUT}" pod_exists "${namespace}" "${pod}"
done
wait_for "java workload pod" "${WAIT_TIMEOUT}" java_workload_pod_exists

log "CASE inject/dns-mode-and-sidecar-config"
pod_json "${NS_INJECT}" dns-ok | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 1 and
  ([.spec.initContainers[]? | select(.name == "polaris-sidecar-init")] | length) == 1 and
  (.metadata.annotations["sidecar.polarismesh.cn/status"] | length) > 0 and
  ([.spec.containers[] | select(.name == "envoy")] | length) == 0 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_DNS_SUFFIX" and .value == "cluster.local")) | length) == 1 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_DNS_TTL" and .value == "30")) | length) == 1 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_REGION" and .value == "beijing")) | length) == 1 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_NEARBY_MATCH_LEVEL" and .value == "zone")) | length) == 1 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_LOG_LEVEL" and .value == "debug")) | length) == 1
' >/dev/null

log "CASE inject/namespace-and-pod-label-selection"
pod_json "${NS_INJECT_POD_LABEL}" pod-label-injected | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 1
' >/dev/null

log "CASE inject/explicit-disable-annotation"
pod_json "${NS_INJECT}" dns-disabled-annotation | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0 and
  ([.spec.initContainers[]? | select(.name | startswith("polaris-"))] | length) == 0
' >/dev/null

log "CASE inject/never-inject-label"
pod_json "${NS_INJECT}" dns-disabled-label | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0 and
  ([.spec.initContainers[]? | select(.name | startswith("polaris-"))] | length) == 0
' >/dev/null

log "CASE inject/dns-hostnetwork-disabled"
pod_json "${NS_INJECT}" dns-hostnetwork | "${JQ}" -e '
  .spec.hostNetwork == true and
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 0
' >/dev/null

log "CASE inject/javaagent-config-priority"
pod_json "${NS_INJECT_JAVA}" java-config | "${JQ}" -e '
  ([.spec.initContainers[]? | select(.name == "polaris-javaagent-init")] | length) == 1 and
  ([.spec.containers[] | select(.name == "app")][0].env |
    map(select(.name == "JAVA_TOOL_OPTIONS" and
      (.value | contains("-Dspring.application.name=java-config-service")) and
      (.value | contains("-Dspring.cloud.polaris.discovery.namespace=it-pc-inject-java")))) | length) == 1
' >/dev/null

log "CASE inject/javaagent-workload-name-fallback"
java_workload_pod="$(kube -n "${NS_INJECT_JAVA}" get pod -l app=java-workload -o jsonpath='{.items[0].metadata.name}')"
pod_json "${NS_INJECT_JAVA}" "${java_workload_pod}" | "${JQ}" -e '
  ([.spec.initContainers[]? | select(.name == "polaris-javaagent-init")] | length) == 1 and
  ([.spec.containers[] | select(.name == "app")][0].env |
    map(select(.name == "JAVA_TOOL_OPTIONS" and
      (.value | contains("-Dspring.application.name=java-workload")) and
      (.value | contains("-Dspring.cloud.polaris.discovery.namespace=it-pc-inject-java")))) | length) == 1
' >/dev/null

log "CASE inject/javaagent-hostnetwork-allowed"
pod_json "${NS_INJECT_JAVA}" java-hostnetwork | "${JQ}" -e '
  .spec.hostNetwork == true and
  ([.spec.initContainers[]? | select(.name == "polaris-javaagent-init")] | length) == 1
' >/dev/null

log "CASE inject/legacy-namespace-label"
pod_json "${NS_INJECT_LEGACY}" legacy-label-injected | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 1
' >/dev/null

log "CASE modes-are-independent"
wait_service_absent "${NS_INJECT}" inject-only-service
wait_service_absent "${NS_INJECT}" dns-ok

log "all injection assertions passed"
