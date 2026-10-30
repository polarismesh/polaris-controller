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
使用 `uninstall_helm.sh` 卸载，将 `${release_name}` 替换为您需要的 release 名。
脚本在删除 helm release 的同时，会清理 controller 在 `polaris-system` 中创建的 sync-state ConfigMap，
以及异常中断卸载后残留的 ClusterRole/ClusterRoleBinding。
```shell
cd deploy
$ ./uninstall_helm.sh polaris-controller
```

脚本默认在当前 kubeconfig 上下文中查找 release；如果安装时使用了 `--namespace`，把该命名空间作为第二个参数传入：
```shell
$ ./uninstall_helm.sh polaris-controller ${release_namespace}
```

controller 所在命名空间默认为 `polaris-system`，可通过 `CONTROLLER_NAMESPACE` 环境变量覆盖。

## 配置
支持的配置可查看helm项目的`values.yaml`文件













