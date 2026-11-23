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
source "${SCRIPT_DIR}/../../lib.sh"

polaris_init

namespace_absent() {
  all_ns_absent "${NS_NS_LIFECYCLE}"
}

if [[ "${RESET_CASES:-0}" == "1" ]]; then
  log "resetting namespace lifecycle case"
  cleanup_case_namespaces
  wait_for "namespace lifecycle case cleanup" "${WAIT_TIMEOUT}" namespace_absent
fi

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap cleanup_case_namespaces EXIT
fi

if namespace_exists "${NS_NS_LIFECYCLE}"; then
  log "Polaris Namespace ${NS_NS_LIFECYCLE} already exists; creation-from-absent check is skipped for this run"
fi

log "CASE lifecycle/namespace-create-sync-and-service"
kube apply -f "${SCRIPT_DIR}/manifests.yaml"
wait_rollout deployment "${NS_NS_LIFECYCLE}" ns-lifecycle-demo
wait_namespace "${NS_NS_LIFECYCLE}"
wait_service "${NS_NS_LIFECYCLE}" ns-lifecycle-demo
wait_instance_count "${NS_NS_LIFECYCLE}" ns-lifecycle-demo 1

log "CASE lifecycle/namespace-delete-retains-polaris-resources"
kube delete namespace "${NS_NS_LIFECYCLE}" --wait=false
wait_for "Kubernetes Namespace ${NS_NS_LIFECYCLE} deletion" "${WAIT_TIMEOUT}" namespace_absent
wait_namespace "${NS_NS_LIFECYCLE}"
service_exists "${NS_NS_LIFECYCLE}" ns-lifecycle-demo || \
  fail "Polaris Service ${NS_NS_LIFECYCLE}/ns-lifecycle-demo was deleted with the Kubernetes Namespace"
wait_instance_count "${NS_NS_LIFECYCLE}" ns-lifecycle-demo 0

log "namespace lifecycle assertions passed"
log "Polaris Namespace ${NS_NS_LIFECYCLE} is intentionally retained after Kubernetes Namespace deletion"
