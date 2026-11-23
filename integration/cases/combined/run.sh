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

combo_namespace_absent() {
  all_ns_absent "${NS_COMBO}"
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting combined namespace"
  cleanup_case_namespaces
  wait_for "combined namespace deletion" "${WAIT_TIMEOUT}" combo_namespace_absent
fi

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap cleanup_case_namespaces EXIT
fi

log "apply combined manifests"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"
wait_rollout deployment "${NS_COMBO}" combo-demo

wait_service "${NS_COMBO}" combo-demo
wait_instance_count "${NS_COMBO}" combo-demo 1

log "CASE combined/service-sync-and-dns-injection"
pod="$(kube -n "${NS_COMBO}" get pod -l app=combo-demo -o jsonpath='{.items[0].metadata.name}')"
kube -n "${NS_COMBO}" get pod "${pod}" -o json | "${JQ}" -e '
  ([.spec.containers[] | select(.name == "polaris-sidecar")] | length) == 1 and
  ([.spec.initContainers[]? | select(.name == "polaris-sidecar-init")] | length) == 1 and
  ([.spec.containers[] | select(.name == "polaris-sidecar")][0].env |
    map(select(.name == "SIDECAR_DNS_TTL" and .value == "15")) | length) == 1
' >/dev/null

assert_service_metadata "${NS_COMBO}" combo-demo owner combo-team
instances_json "${NS_COMBO}" combo-demo | "${JQ}" -e '
  .instances[0].metadata.tenant == "combo-pod-tenant" and
  .instances[0].metadata.env == "combo" and
  .instances[0].version == "v1" and
  .instances[0].metadata.source == "polaris-controller"
' >/dev/null

log "combined service-sync and injection assertions passed"
