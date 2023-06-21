#!/bin/bash
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

set -e

if [ $# -gt 0 ]; then
    version="$1"
else
    current=$(date "+%Y-%m-%d %H:%M:%S")
    timeStamp=$(date -d "$current" +%s)
    currentTimeStamp=$(((timeStamp * 1000 + 10#$(date "+%N") / 1000000) / 1000))
    version="$currentTimeStamp"
fi
workdir=$(dirname $(realpath $0))

sed -i "s/##VERSION##/$version/g" "$workdir"/deploy/variables.txt
cat "$workdir"/deploy/variables.txt

folder_name="polaris-controller-release_${version}.k8s1.21"
pkg_name="${folder_name}.zip"

cd $workdir

# 清理环境
rm -rf ${folder_name}
rm -f "${pkg_name}"

# 打包
mkdir -p ${folder_name}
cp -r deploy/kubernetes_v1.21/* ${folder_name}
cp deploy/variables.txt ${folder_name}
zip -r "${pkg_name}" ${folder_name}
#md5sum ${pkg_name} > "${pkg_name}.md5sum"

if [[ $(uname -a | grep "Darwin" | wc -l) -eq 1 ]]; then
    md5 ${pkg_name} >"${pkg_name}.md5sum"
else
    md5sum ${pkg_name} >"${pkg_name}.md5sum"
fi

folder_name="polaris-controller-release_${version}.k8s1.22"
pkg_name="${folder_name}.zip"

cd $workdir

# 清理环境
rm -rf ${folder_name}
rm -f "${pkg_name}"

# 打包
mkdir -p ${folder_name}
cp -r deploy/kubernetes_v1.22/* ${folder_name}
cp deploy/variables.txt ${folder_name}
zip -r "${pkg_name}" ${folder_name}
#md5sum ${pkg_name} > "${pkg_name}.md5sum"

if [[ $(uname -a | grep "Darwin" | wc -l) -eq 1 ]]; then
    md5 ${pkg_name} >"${pkg_name}.md5sum"
else
    md5sum ${pkg_name} >"${pkg_name}.md5sum"
fi
