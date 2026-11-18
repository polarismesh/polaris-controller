#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${SCRIPT_DIR}/../../deploy.sh" install manifests \
  --kubernetes-version v1.22 \
  --variables-file "${SCRIPT_DIR}/variables.txt" \
  "$@"
