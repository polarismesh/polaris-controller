#!/bin/sh
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


# 借鉴于 istio-iptables --redirect-dns 的规则

iptables -t nat -P PREROUTING ACCEPT
iptables -t nat -P INPUT ACCEPT
iptables -t nat -P OUTPUT ACCEPT
iptables -t nat -P POSTROUTING ACCEPT
iptables -t nat -N POLARIS_INBOUND
iptables -t nat -N POLARIS_OUTPUT
iptables -t nat -N POLARIS_REDIRECT
iptables -t nat -A OUTPUT -p tcp -j POLARIS_OUTPUT
iptables -t nat -A OUTPUT -p udp -m udp --dport 53 -m owner --uid-owner 1337 -j RETURN
iptables -t nat -A OUTPUT -p udp -m udp --dport 53 -m owner --gid-owner 1337 -j RETURN
iptables -t nat -A OUTPUT -p udp -m udp --dport 53 -j REDIRECT --to-ports 15053
iptables -t nat -A POLARIS_OUTPUT -s 127.0.0.6/32 -o lo -j RETURN
iptables -t nat -A POLARIS_OUTPUT -o lo -p tcp -m tcp ! --dport 53 -m owner ! --uid-owner 1337 -j RETURN
iptables -t nat -A POLARIS_OUTPUT -m owner --uid-owner 1337 -j RETURN
iptables -t nat -A POLARIS_OUTPUT -o lo -p tcp -m tcp ! --dport 53 -m owner ! --gid-owner 1337 -j RETURN
iptables -t nat -A POLARIS_OUTPUT -m owner --gid-owner 1337 -j RETURN
iptables -t nat -A POLARIS_OUTPUT -p tcp -m tcp --dport 53 -j REDIRECT --to-ports 15053
iptables -t nat -A POLARIS_OUTPUT -d 127.0.0.1/32 -j RETURN

# 处理polaris sidecar配置文件
printenv polaris-client-config > /data/polaris-client-config/polaris.yaml
