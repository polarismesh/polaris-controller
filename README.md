# Polaris Controller

## 概览

Polaris Controller 是 Polaris 网格方案中的组件，部署在每个需要接入到 Polaris 的 k8s 集群中。提供将 k8s 服务自动注册到 Polaris ，同时也提供了自动注入 Sidecar 的能力。

本文档将介绍如何在 k8s 集群上安装、配置 Polaris Controller 。

## 环境准备

您需要先下载 Polaris 并启动，相信可参考[服务端安装指南](https://github.com/PolarisMesh/website/blob/main/docs/zh/doc/%E5%BF%AB%E9%80%9F%E5%85%A5%E9%97%A8/%E5%AE%89%E8%A3%85%E6%9C%8D%E5%8A%A1%E7%AB%AF.md)

## 安装 Polaris Controller

### 获取 Polaris Controller 代码

```
git clone https://github.com/PolarisMesh/polaris-controller.git

cd polaris-controller/deploy/polaris-controller
```

### 配置 Polaris Server 地址和集群信息

修改 configmap.yaml ，配置下面的参数：

- 将 `POLARIS_SERVER_URL` 替换为 Polaris server 的 ip。Polaris Controller 会将 k8s service 同步到这个 Poarlis server。
- 将 `CLUSTER_NAME` 替换为您需要的值。北极星支持多集群接入的方案，即多个集群的同名服务，同步到北极星是一个服务，通过北极星的服务发现，可以发现两个 k8s 的同名服务的实例列表。当使用多集群方案时，您需要为两个集群指定不同的 CLUSTER_NAME ， Polaris Controller 会只处理自己所在的集群的实例。

### 运行安装脚本

您需要在安装了 kubectl 的机器上运行下面的脚本：

```
bash ./install.sh 
```

使用下面的命令观察 Polaris Controller 是否已经正常运行：

```
kubectl get pod -n polaris-system
```

正常是可以看到：

```
NAME                                  READY   STATUS    RESTARTS   AGE
polaris-controller-545df9775c-48cqt   1/1     Running   0          2d9h
```

### 验证服务同步功能

Polaris Controller 正常运行后，登录 Polaris 的控制台，可以看到 k8s 集群中，当前的 namespace、service、endpoints 信息已经同步到了北极星中。

例如您在 samples 命名空间下部署了 istio 的 bookinfo demo，安装完 Polaris Controller 后，可以从 Polaris 的控制台上看到，北极星的命名空间中增加了 samples ，且 samples 下有 reviews、productpage、ratings、details 这四个北极星服务，每个服务下有一个北极星实例，ip 为 k8s 集群的 pod ip。

### 服务同步配置
Polaris Controller 允许您在 Service 的 Annotations 上配置参数，来控制服务同步的行为，目前支持的参数如下：

- polaris.cloud.tencentyun.com/autoregister: 如果某些服务您不想由 Poalris Controller 注册到北极星，可以设置 polaris.cloud.tencentyun.com/autoregister=true。
- polaris.cloud.tencentyun.com/metadata: 如果您希望 Poalris Controller 注册的实例带上特定的 metadata ，可以指定这个参数，例如 polaris.cloud.tencentyun.com/metadata='{"version":"v1"}'。

### 开启自动注入功能

以下命令为 `default` 命名空间启用注入，`Polaris Controller` 的注入器，会将 `Envoy Sidecar` 容器注入到在此命名空间下创建的 pod 中：

```
kubectl label namespace default polaris-injection=enabled 
```

使用一下命令来验证 `default` 命名空间是否已经正确启用：

```
kubectl get namespace -L polaris-injection
```

此时应该返回：

```
NAME             STATUS   AGE    POLARIS-INJECTION
default          Active   3d2h   enabled
```

这时已经打开了自动注入，新创建的 pod 会自动被注入 sidecar 容器。存量的 pod 需要删除重建来触发自动注入。如果您使用的是工作负载，可以使用下面的命令滚动更新：

```
kubectl rollout restart deployment xxx --namespace xxxx
```

### 自动注入配置

Polaris Controller 允许您在 pod 的 Annotations 中指定一些配置来控制自动注入的行为，目前支持的配置有：

- annotation sidecar.istio.io/inject: 您可以对命名空间开启自动注入，并对某些特殊的 pod 配置 annotation sidecar.istio.io/inject=false ，关闭这个 pod 的自动注入。
- cloud.tencent.com/proxyCPU: 自动注入的 Envoy 的 cpu request。
- cloud.tencent.com/proxyMemory: 自动注入的 Envoy 的 memory request。
- cloud.tencent.com/proxyCPULimit: 自动注入的 Envoy 的 cpu limit。
- cloud.tencent.com/proxyMemoryLimit: 自动注入的 Envoy 的 memory limit。
