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

# Print retained integration resources for manual inspection.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

require_bin "${KUBECTL}"

found=0
while IFS= read -r namespace; do
  if ! kube get namespace "${namespace}" --request-timeout=10s >/dev/null 2>&1; then
    continue
  fi
  found=1
  printf '\n===== %s =====\n' "${namespace}"
  kube -n "${namespace}" get all,endpoints --ignore-not-found --request-timeout=10s || \
    printf '[inspect] unable to read all resources in %s\n' "${namespace}" >&2
done < <(case_namespace_names)

if ((found == 0)); then
  printf '[inspect] no retained integration namespaces\n'
else
  printf '\n[inspect] cleanup: %s/cleanup.sh\n' "${SCRIPT_DIR}"
fi
