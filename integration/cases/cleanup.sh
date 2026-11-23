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
source "${SCRIPT_DIR}/lib.sh"

log "delete dedicated case namespaces"
cleanup_case_namespaces

case_namespaces_absent() {
  all_ns_absent \
    "${NS_SYNC}" "${NS_SYNC_LEGACY}" "${NS_SYNC_ALL}" \
    "${NS_INJECT}" "${NS_INJECT_POD_LABEL}" "${NS_INJECT_JAVA}" "${NS_INJECT_LEGACY}" \
    "${NS_INJECT_NO_MODE}" "${NS_COMBO}" \
    "${NS_NS_LIFECYCLE}" "${NS_SVC_LIFECYCLE}" "${NS_SVC_ALIAS}" "${NS_EP_LIFECYCLE}"
}

wait_for "case namespace cleanup" "${WAIT_TIMEOUT}" case_namespaces_absent

log "case namespaces removed"
log "Polaris aliases and Namespaces are intentionally not deleted automatically; verify/clean metadata-demo-alias, shared-alias, and it-pc-ns-lifecycle manually if needed"
