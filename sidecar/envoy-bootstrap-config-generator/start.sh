#!/bin/bash
#  Tencent is pleased to support the open source community by making Polaris available.
#
#  Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
#
#  Licensed under the BSD 3-Clause License (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#  https://opensource.org/licenses/BSD-3-Clause
#
#  Unless required by applicable law or agreed to in writing, software distributed
#  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
#  CONDITIONS OF ANY KIND, either express or implied. See the License for the
#  specific language governing permissions and limitations under the License.

set -ex
readonly PACKAGE_DIRECTORY=$(dirname "$0")
BOOTSTRAP_TEMPLATE="${PACKAGE_DIRECTORY}/bootstrap_template.yaml"
readonly BOOTSTRAP_INSTANCE="${PACKAGE_DIRECTORY}/bootstrap_instance.yaml"

function prepare_envoy() {
  # Generate Envoy bootstrap.
  # namespace/$uuidgen~$tlsmode~$hostname
  envoy_node_id="sidecar~${NAMESPACE}/${POD_NAME}~${INSTANCE_IP}"
  if [[ -v TLS_MODE ]]
  then
    BOOTSTRAP_TEMPLATE="${PACKAGE_DIRECTORY}/bootstrap_template_tls.yaml"
  fi 
  cat "${BOOTSTRAP_TEMPLATE}" \
      | sed -e "s|ENVOY_NODE_ID|${envoy_node_id}|g" \
      | sed -e "s|CLUSTER_NAME|${CLUSTER_NAME}|g" \
      | sed -e "s|POLARIS_SERVER_URL|${POLARIS_SERVER_URL}|g" \
      | sed -e "s|POLARIS_SERVER_HOST|${POLARIS_SERVER_HOST}|g" \
      | sed -e "s|POLARIS_SERVER_PORT|${POLARIS_SERVER_PORT}|g" \
      | sed -e "s|METADATA|${METADATA}|g" \
      > "${BOOTSTRAP_INSTANCE}"
}

printenv polaris-client-config > /data/polaris-client-config/polaris.yaml

prepare_envoy

if  [[ -v DEBUG_MODE ]]
then
   cat "${BOOTSTRAP_INSTANCE}"
fi

mv "${BOOTSTRAP_INSTANCE}" /var/lib/data/envoy.yaml
