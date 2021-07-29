#!/bin/bash

ROOT=$(cd $(dirname $0)/../../; pwd)

set -o errexit
set -o nounset
set -o pipefail


export CLUSTER=$(cat /etc/kubernetes/config  | grep KUBE_CLUSTER | sed -r 's/.*"(.+)".*/\1/')

if command -v envsubst >/dev/null 2>&1; then
    envsubst
else
    sed -e "s|\${CLUSTER}|${CLUSTER}|g"
fi