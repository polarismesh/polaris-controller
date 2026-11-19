#!/usr/bin/env bash

# Usage:
#   ./run-all.sh
#   CLEANUP=1 RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh
#
# Environment:
#   RESET_CASES=1       delete dedicated namespaces once before running (default)
#   CLEANUP=1           delete dedicated namespaces when the run exits
#   RUN_ALIAS_WINNER=1  verify alias stable winner and handover
#   RUN_ALL_MODE=1      switch controller to all mode, verify, then restore
#   POLARIS_HTTP_URL    override Polaris HTTP API URL
#   POLARIS_TOKEN       override token; otherwise read from injector-mesh ConfigMap

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

polaris_init

wait_rollout statefulset polaris-system polaris-controller

all_namespaces_absent() {
  all_ns_absent \
    "${NS_SYNC}" "${NS_SYNC_LEGACY}" "${NS_SYNC_ALL}" \
    "${NS_INJECT}" "${NS_INJECT_POD_LABEL}" "${NS_INJECT_JAVA}" "${NS_INJECT_LEGACY}" \
    "${NS_COMBO}"
}

for bin in "${KUBECTL}" "${CURL}" "${JQ}"; do
  require_bin "${bin}"
done

if [[ "${CLEANUP:-0}" == "1" ]]; then
  trap "${SCRIPT_DIR}/cleanup.sh" EXIT
fi

if [[ "${RESET_CASES:-1}" == "1" ]]; then
  log "reset all dedicated case namespaces once"
  cleanup_case_namespaces
  wait_for "initial case namespace cleanup" "${WAIT_TIMEOUT}" all_namespaces_absent
fi
export RESET_CASES=0

log "run Kubernetes Service sync cases"
"${SCRIPT_DIR}/service-sync/run.sh"

if [[ "${RUN_ALIAS_WINNER:-0}" == "1" ]]; then
  log "run alias winner and handover case"
  "${SCRIPT_DIR}/service-sync/alias-winner.sh"
fi

log "run sidecar injection cases"
"${SCRIPT_DIR}/inject/run.sh"

log "run combined sync and injection case"
"${SCRIPT_DIR}/combined/run.sh"

if [[ "${RUN_ALL_MODE:-0}" == "1" ]]; then
  log "run all-mode service sync case"
  "${SCRIPT_DIR}/service-sync/switch-mode.sh" all
fi

log "all cases completed"
