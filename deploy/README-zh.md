# Polaris Controller helm安装文档

简体中文 | [English](./README.md)

本文档介绍如何使用 helm chart 安装 polaris-controller 服务。

## 准备工作

确保已经安装 k8s 集群，且安装了 helm。
在polaris-system命名空间下已经成功部署polaris server服务
(参考文档见: [helm部署北极星](https://github.com/polarismesh/polaris/tree/main/release/cluster/helm))

## 安装
### 初始化helm配置
确认`deploy/variables.txt`文件中的变量赋值符合预期,示例如下
```shell
cd deploy
$ cat variables.txt                                                                                                                                                                                                                                                                               deploy -> main ? ! |•
POLARIS_HOST:polaris.polaris-system
CONTROLLER_VERSION:v1.7.1
SIDECAR_VERSION:v1.5.1
POLARIS_TOKEN:nu/0WRA4EqSR1FagrjRj0fZwPXuGlMpX+zCuWu4uMqy8xr1vRjisSbA25aAC3mtU8MeeRsKhQiDAynUR09I=
ENVOY_VERSION:v1.26.2
CLUSTER_NAME:default
JAVA_AGENT_INIT:v0.0.1% 
```
初始化helm项目的`values.yaml`文件
```shell
sh init_helm.sh
```

### 部署
使用`helm install ${release_name} .`命令安装，将 `${release_name}` 替换为您需要的 release 名。示例如下
```shell
cd helm
helm install polaris-controller .
```

### 更新
使用`helm upgrade -i ${release_name} .`命令更新，将 `${release_name}` 替换为您需要的 release 名。示例如下
```shell
helm upgrade -i polaris-controller .
```

### 卸载
使用`helm uninstall `${release_name}``命令更新，将 `${release_name}` 替换为您需要的 release 名。示例如下
```shell
$ helm uninstall polaris-controller
```

## 配置
支持的配置可查看helm项目的`values.yaml`文件













