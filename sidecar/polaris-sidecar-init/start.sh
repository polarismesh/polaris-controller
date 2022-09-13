#!/bin/sh

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
