#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log "delete dedicated case namespaces"
cleanup_case_namespaces

case_namespaces_absent() {
  all_ns_absent \
    "${NS_SYNC}" "${NS_SYNC_LEGACY}" "${NS_SYNC_ALL}" \
    "${NS_INJECT}" "${NS_INJECT_POD_LABEL}" "${NS_INJECT_JAVA}" "${NS_INJECT_LEGACY}" \
    "${NS_COMBO}"
}

wait_for "case namespace cleanup" "${WAIT_TIMEOUT}" case_namespaces_absent

log "case namespaces removed"
log "Polaris aliases are intentionally not deleted automatically; verify/clean metadata-demo-alias and shared-alias manually if needed"
