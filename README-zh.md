# Polaris Controller

[English](./README.md) | 中文

  - [介绍](#介绍)
  - [与Kubernetes的版本支持](#与kubernetes的版本支持)
  - [安装说明](#安装说明)
  - [部署与使用限制](#部署与使用限制)
  - [注解](#注解)
  - [使用指南](#使用指南)
    - [全量同步服务](#全量同步服务)
    - [按需同步服务](#按需同步服务)
    - [创建服务别名](#创建服务别名)
    - [Sidecar 自动注入](#sidecar-自动注入)

## 介绍

polaris-controller 用于北极星和 K8s 生态的对接，提供两个可选功能：

- K8s Service 同步到北极星：将 K8s Service 同步到北极星，使用北极星进行服务发现和治理
- polaris-sidecar 自动注入：在应用 Pod 中注入 polaris-sidecar

根目录文档只介绍本地 DNS 模式的 Sidecar 注入：

- 本地 DNS（dns）：注入 **polaris-sidecar**，通过拦截 DNS 请求的方式实现服务发现和治理

本文档介绍如何在 K8s 集群中安装和使用 polaris-controller。

## 与Kubernetes的版本支持

- Kubernetes `v1.21` 使用 `v1.21` 清单；`v1.22` 及以上版本统一复用 `v1.22` 清单。补丁版本和厂商版本后缀（例如 `v1.28.5+k3s1`）会归一化到 `v1.22` 清单。

## 安装说明

**前提条件**

在安装 polaris-controller 前，请先安装北极星服务端，安装方式请参考[北极星服务端安装文档](https://polarismesh.cn/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF/%E5%AE%89%E8%A3%85%E5%8D%95%E6%9C%BA%E7%89%88.html)。

**下载安装包**

从 [Releases](https://github.com/polarismesh/polaris-controller/releases) 下载最新版本的安装包。

**修改配置文件**

修改 `polaris-system` 下 controller ConfigMap 中的配置，至少设置北极星服务端地址、集群名和 K8s Service 同步模式；开启鉴权时还需设置 token 和 operator。若使用 Sidecar DNS 模式，将 `sidecarInject.mode` 设置为 `dns`。完整字段说明见 [用户指南](./docs/README.md)。

支持两种 K8s Service 同步模式：

- all：全量同步服务。将 K8s Service 全部同步到北极星。
- demand：按需同步服务。默认不会将 K8s Service 同步到北极星，需要在 Namespace 或者 Service 上添加北极星的 annotation。

北极星支持跨 K8s 集群的服务发现和治理，多个 K8s 集群的 Service 可以同步到一个北极星集群，同步规则如下：

- K8s Namespace 和 Service 名称作为北极星的命名空间名称
- 如果多个 K8s 集群存在相同的 Namespace 和 Service，全部 Pod 同步到一个北极星服务中
- polaris-controller 在北极星服务实例上添加 clusterName 标签，用于区分来自不同 K8s 集群的服务实例
- 如果存在多个 K8s Service 同步到一个北极星服务的情况，每个 K8s 集群的 polaris-controller 需要配置不同的 clusterName

**运行安装脚本**

在安装 kubectl 的机器上运行安装脚本：

```shell
bash ./install.sh 
```

controller 只能部署在 `polaris-system` Namespace，标准 Helm Chart 和原生 manifest 已按此 Namespace 配置。不要通过自定义 Namespace 安装 controller。

查看 polaris-controller 是否正常运行：

```shell
kubectl get pod -n polaris-system

NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

## 部署与使用限制

- **controller 只能部署在 `polaris-system` Namespace。** Service 同步账本固定使用该 Namespace 下的 16 个 `polaris-controller-sync-state-*` ConfigMap；账本、RBAC、leader election、`POD_NAMESPACE` 和卸载脚本必须使用同一个 Namespace，因此不支持自定义 controller Namespace。
- 该限制只针对 controller 自身。需要同步的业务 Kubernetes Service 仍可位于其他业务 Namespace。
- 不要手工修改或删除 sync-state ConfigMap。这些 ConfigMap 保存 Service 同步归属、已应用 Label 和待删除恢复状态，用于 controller 重启及 leader 切换后的恢复。
- ConfigMap/配置同步属于实验功能，当前未开放生产。不要在生产环境启用，也不要依赖其 Annotation、资源格式或故障恢复行为。
- 通过 Kubernetes Service 同步到 Polaris 的 Service 和实例由 Kubernetes 声明管理，不支持在 Polaris 控制台编辑或修改。需要变更时应修改 Kubernetes Service、Endpoints、Pod Label 或 Annotation。
- 兼容性只以 `main` 分支的既有行为为准。开发分支自身的中间实现不作为兼容契约，可以直接纠正。

## 注解

| 注解名称                      | 注解描述                                                            |
| ----------------------------- | ------------------------------------------------------------------- |
| polarismesh.cn/sync           | 是否同步这个服务到 polarismesh。true 同步，false 不同步，默认不同步 |
| polarismesh.cn/aliasService   | 把 k8s service 同步到 polarismesh 时，同时创建的服务别名的名字      |
| polarismesh.cn/aliasNamespace | 创建的别名所在的命名空间，配合 polarismesh.cn/aliasService 使用     |

## 使用指南

### 全量同步服务

以全量同步服务的模式启动 polaris-controller，将 K8s Service 全部同步到北极星，启动配置如下：

```yaml
serviceSync:
  mode: "all"
```

### 按需同步服务

以按需同步服务的模式启动 polaris-controller，默认不会将 K8s Service 同步到北极星，启动配置如下：

```yaml
serviceSync:
  mode: "demand"
```

如果需要将某个 Namespace 中的全部 Service 同步到北极星，请在 Namespace 上添加北极星的 annotation，配置方式如下： 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"
```

如果需要将某个 Service 同步到北极星，请在 Service 上添加北极星的 annotation，配置方式如下： 

```yaml
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/sync: "true"
```

如果需要将某个 Namespace 中的 Service同步到北极星并且排除某个 Service，配置方式如下： 

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: default
  annotations:
    polarismesh.cn/sync: "true"

---
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/sync: "false"
```

### 创建服务别名

北极星支持服务别名的功能，允许为一个服务设置一个或者多个服务别名，使用服务别名进行服务发现的效果等同使用服务名称进行服务发现的效果。

polaris-controller 将 K8s Service 同步到北极星的名称映射规则如下：

- K8s Namespace作为北极星的命名空间名称
- K8s Service作为北极星的服务名称

如果需要在 Service 同步到北极星时，为其创建一个服务别名，配置方式如下：

```yaml
apiVersion: v1
kind: Service
metadata:
  namespace: default
  name: test
  annotations:
    polarismesh.cn/aliasNamespace: aliasDefault
    polarismesh.cn/aliasService: aliasTest
```

### 实例同步

`polarismesh.cn/enableRegister: "true"` 作为历史同步开关保留。
Service 未设置 `polarismesh.cn/sync` 时，`enableRegister=true` 与 `sync=true` 一样会开启 Service 同步，
controller 仍会正常新增、更新和删除实例。
如果同时设置了 `polarismesh.cn/sync`，以 `sync` 为准；例如 `sync=false` 时，
即使 `enableRegister=true` 也不会同步该 Service。

实例 metadata 由 `polarismesh.cn/metadata` 和 Pod labels 合并，同名字段以 Pod labels 为准。
`source`、`clusterName` 为受保护字段。其余业务字段按完整集合管理：从两处声明中移除字段后，
下一次更新会删除实例上的对应字段，包括直接在北极星添加的字段。
兼容已有 `version`、`protocol` 和旧版 `platform` metadata：两处都未声明时保留现值。
Pod labels 的独立变更通过周期对账收敛，周期由 `--resync-duration` 指定，默认为 `30s`。

### Sidecar 自动注入

> 注意：ConfigMap/配置同步功能当前处于 Beta 阶段，请勿在生产环境使用。

如果需要使用 polaris-sidecar，可以在应用 Pod 中自动注入，配置方式如下：

```shell
kubectl label namespace default polaris-injection=enabled

# 根目录文档只介绍 DNS 模式
kubectl label namespace default polaris-sidecar-mode=dns
```

查看 K8s Namespace 是否支持 polaris-sidecar 自动注入：

```shell
kubectl get namespace -L polaris-injection

NAME             STATUS   AGE    POLARIS-INJECTION
default          Active   3d2h   enabled
```

查看 K8s Namespace 对应的 polaris-sidecar 注入之后的运行模式：

```shell
kubectl get namespace -L polaris-sidecar-mode

NAME              STATUS   AGE   POLARIS-SIDECAR-MODE
default           Active   10d   dns
```

在开启自动注入后，新建的 Pod 会自动注入，存量的 Pod 不会自动注入 polaris-sidecar。如果希望存量的 Pod 也能体验 polaris-sidecar， 则对于由 Deployment、DaemonSet 或 StatefulSet 控制器管理的 pod，可以运行以下命令

```shell
# Deployment
kubectl rollout restart deployment/DEPLOYMENT_NAME --namespace NAMESPACE

# DaemonSet
kubectl rollout restart daemonset/DAEMONSET_NAME --namespace NAMESPACE

# StatefulSet
kubectl rollout restart statefulset/STATEFULSET_NAME --namespace NAMESPACE
```

