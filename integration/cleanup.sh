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

# Clean integration test resources from the current bundle.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "${SCRIPT_DIR}/cases/cleanup.sh" ]]; then
  CASES_DIR="${SCRIPT_DIR}/cases"
elif [[ -f "${SCRIPT_DIR}/integration/cases/cleanup.sh" ]]; then
  CASES_DIR="${SCRIPT_DIR}/integration/cases"
else
  printf '[FAIL] cannot locate integration/cases/cleanup.sh\n' >&2
  exit 1
fi

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<'EOF'
Usage: ./cleanup.sh

Delete all dedicated integration test Namespaces.
EOF
  exit 0
fi
if (($# > 0)); then
  printf '[FAIL] cleanup.sh does not accept arguments\n' >&2
  exit 1
fi

exec "${CASES_DIR}/cleanup.sh" "$@"
