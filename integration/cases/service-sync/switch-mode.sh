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

target_mode="${1:-all}"
case "${target_mode}" in
  all|demand) ;;
  *) fail "usage: $0 <all|demand>" ;;
esac

polaris_init

get_mode() {
  kube -n polaris-system get configmap injector-mesh -o jsonpath='{.data.mesh}' | awk '
    /^serviceSync:/ {inside=1; next}
    inside && /^  mode:/ {print $2; exit}
  '
}

set_mode() {
  local mode="$1"
  local mesh new_mesh
  mesh="$(kube -n polaris-system get configmap injector-mesh -o jsonpath='{.data.mesh}')"
  new_mesh="$(printf '%s\n' "${mesh}" | awk -v mode="${mode}" '
    /^serviceSync:[[:space:]]*$/ {inside=1; print; next}
    inside && /^  mode:[[:space:]]*/ {
      sub(/mode:[[:space:]]*.*/, "mode: " mode)
      inside=0
      print
      next
    }
    {print}
  ')"
  [[ "${new_mesh}" != "${mesh}" ]] || return 0
  kube -n polaris-system patch configmap injector-mesh --type merge \
    -p "$("${JQ}" -cn --arg mesh "${new_mesh}" '{data:{mesh:$mesh}}')"
  kube -n polaris-system rollout restart statefulset/polaris-controller
  wait_rollout statefulset polaris-system polaris-controller
}

original_mode="$(get_mode)"
[[ -n "${original_mode}" ]] || fail "cannot determine current serviceSync.mode"

restore_mode() {
  if [[ "${KEEP_MODE:-0}" != "1" && "${original_mode}" != "$(get_mode)" ]]; then
    log "restore serviceSync.mode=${original_mode}"
    set_mode "${original_mode}"
  fi
}
trap restore_mode EXIT

log "switch serviceSync.mode ${original_mode} -> ${target_mode}"
set_mode "${target_mode}"

if [[ "${target_mode}" == "all" ]]; then
  wait_service "${NS_SYNC_ALL}" all-mode-demo
  wait_instance_count "${NS_SYNC_ALL}" all-mode-demo 1
else
  wait_service_absent "${NS_SYNC_ALL}" all-mode-demo
fi

log "serviceSync.mode=${target_mode} assertion passed"

if [[ "${KEEP_MODE:-0}" != "1" && "${original_mode}" != "${target_mode}" ]]; then
  set_mode "${original_mode}"
  if [[ "${original_mode}" == "demand" ]]; then
    wait_service_absent "${NS_SYNC_ALL}" all-mode-demo
  fi
  trap - EXIT
  log "restored serviceSync.mode=${original_mode}"
fi
