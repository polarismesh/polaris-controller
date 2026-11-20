# Polaris Controller 集成测试

本目录用于验证 Polaris Controller 的真实 Kubernetes 与 Polaris 行为。测试会
创建真实 Kubernetes 资源并查询已部署的 Polaris 实例，不依赖模拟的
Kubernetes 或 Polaris 后端。

测试覆盖的功能说明见 [Polaris Controller 用户指南](../docs/README.md)。

## 使用警告

脚本使用固定的 Kubernetes Namespace 名称，完整运行默认会先删除这些
Namespace。请勿在共享集群中使用相同 Namespace 名称运行测试。

这些用例用于在本地验证当前分支与已部署组件的兼容性。本地通过不等同于 CI
通过或正式发布验收。

## 目录结构

```text
integration/
├── README.md
├── README-zh.md
└── cases/
    ├── lib.sh                     # 公共函数，不是测试入口
    ├── run-all.sh                 # 完整测试入口
    ├── cleanup.sh                 # 删除全部测试 Namespace
    ├── service-sync/
    │   ├── manifests.yaml
    │   ├── run.sh                 # Kubernetes Service 同步用例
    │   ├── alias-winner.sh        # 别名稳定目标和切换用例
    │   └── switch-mode.sh         # serviceSync.mode 切换用例
    ├── lifecycle/
    │   ├── namespace/
    │   │   ├── manifests.yaml
    │   │   └── run.sh             # Namespace 生命周期
    │   ├── service/
    │   │   ├── manifests.yaml
    │   │   └── run.sh             # Service 生命周期
    │   └── endpoints/
    │       ├── manifests.yaml
    │       └── run.sh             # Endpoints 生命周期
    ├── inject/
    │   ├── manifests.yaml
    │   └── run.sh                 # Sidecar 和 Java Agent 注入用例
    └── combined/
        ├── manifests.yaml
        └── run.sh                 # Service 同步和注入组合用例
```

## 前置条件

运行前请确认：

1. Polaris Controller 已部署在 `polaris-system` Namespace。
   `run-all.sh` 会等待 `statefulset/polaris-controller` 完成滚动更新。
2. `polaris-system/injector-mesh` ConfigMap 存在，并包含 `serverAddress`，
   需要鉴权时还要包含 `accessToken`。
3. 当前 Kubernetes context 有权限创建和删除 Namespace、创建和修改工作负载
   与 Service、读取 Endpoints、修改 `injector-mesh` ConfigMap，以及重启
   controller StatefulSet。
4. 执行脚本的机器可以直接访问 Polaris HTTP API。未指定时，脚本从
   `injector-mesh` 读取地址，并拼接为 `http://<serverAddress>:8090`。
5. Polaris Token 有权限查询 Namespace、Service、实例和别名，并能在 Service
   生命周期用例中注册和删除测试实例。
6. 已安装 `bash`、`kubectl`、`curl` 和 `jq`。
7. 集群可以从 `ccr.ccs.tencentyun.com` 拉取测试镜像。部分注入用例还需要
   拉取该仓库中的 Java Agent 测试镜像。
8. 集群允许创建 NodePort 和 LoadBalancer Service，Service 生命周期用例会
   覆盖这两种类型。

运行前可以先检查集群和对象：

```bash
kubectl config current-context
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system get configmap injector-mesh
command -v bash kubectl curl jq
```

`KUBECTL`、`CURL` 和 `JQ` 只接受可执行文件名或路径。需要切换集群时，建议先
切换 kubectl context，或者设置例如 `KUBECTL=/path/to/kubectl`。不要在
`KUBECTL` 中追加 kubectl 参数，因为脚本会把整个值当作一个可执行文件名称。

## 快速开始

在 `cases` 目录执行默认用例：

```bash
cd integration/cases
./run-all.sh
```

默认运行会：

- 检查 controller 是否完成 rollout；
- 清理一次测试 Namespace；
- 运行 Service 同步用例；
- 运行 Namespace、Service 和 Endpoints 生命周期用例；
- 运行 Sidecar 和 Java Agent 注入用例；
- 运行 Service 同步与注入组合用例；
- 保留其余测试 Namespace 和工作负载，便于排查。

下面的命令会额外运行别名稳定目标用例和全量模式用例，并在退出时清理测试
Namespace：

```bash
CLEANUP=1 RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh
```

`RUN_ALL_MODE=1` 会临时把 `serviceSync.mode` 改为 `all`，重启 controller，
完成断言后再恢复原配置。该选项会修改共享控制器配置，不要在共享环境直接使用。

## 环境变量

常用环境变量如下：

| 变量 | 默认值 | 作用范围 | 说明 |
| --- | --- | --- | --- |
| `POLARIS_HTTP_URL` | `http://<serverAddress>:8090` | Polaris 断言 | 覆盖 Polaris HTTP API 地址。API 使用其他端口、代理或外部地址时使用。 |
| `POLARIS_TOKEN` | 从 `injector-mesh` 读取 | Polaris 断言 | 覆盖 Polaris API Token。显式设为空字符串时不发送 Token 请求头。 |
| `KUBECTL` | `kubectl` | 全部用例 | kubectl 可执行文件名或路径。 |
| `CURL` | `curl` | Polaris 断言 | curl 可执行文件名或路径。 |
| `JQ` | `jq` | Polaris 断言 | jq 可执行文件名或路径。 |
| `WAIT_TIMEOUT` | `120` | 全部用例 | 等待 rollout、Pod 和 Namespace 状态的超时时间，单位秒。 |
| `RESYNC_TIMEOUT` | `180` | 全部用例 | 等待 controller 对账和 Polaris 断言完成的超时时间，单位秒。 |
| `EXTERNAL_DELETE_TIMEOUT` | `30` | `lifecycle/service` | 清理手工注册的外部实例的超时时间，单位秒。 |
| `RESET_CASES` | `run-all.sh` 为 `1`；单用例为 `0` | 各测试入口 | 应用 manifest 前是否删除固定测试 Namespace。 |
| `CLEANUP` | `0` | 各测试入口 | 进程退出时是否删除固定测试 Namespace。 |
| `RUN_ALIAS_WINNER` | `0` | `run-all.sh` | 是否在 Service 同步用例之后运行 `alias-winner.sh`。 |
| `RUN_ALL_MODE` | `0` | `run-all.sh` | 是否在其他用例之后运行 `switch-mode.sh all`；退出时会尝试恢复原模式。 |
| `KEEP_MODE` | `0` | `switch-mode.sh` | 设为 `1` 时不恢复原 `serviceSync.mode`，仅用于调试。 |

指定 API 地址和更长超时的示例：

```bash
POLARIS_HTTP_URL="http://10.0.0.10:8090" \
POLARIS_TOKEN="<polaris-token>" \
WAIT_TIMEOUT=180 \
RESYNC_TIMEOUT=300 \
./run-all.sh
```

`lib.sh` 中的 `NS_*` 变量是测试资源定义，不是通用的隔离手段。manifest 中写死
了 `it-pc-sync`、`it-pc-inject-default` 等名称；只修改环境变量而不修改
manifest，断言会查询与实际创建资源不同的 Namespace。

## 脚本说明

### `run-all.sh`

完整测试入口。它会检查前置条件、执行初始清理，并按顺序调用：

1. `service-sync/run.sh`
2. `lifecycle/namespace/run.sh`
3. `lifecycle/service/run.sh`
4. `lifecycle/endpoints/run.sh`
5. `RUN_ALIAS_WINNER=1` 时调用 `service-sync/alias-winner.sh`
6. `inject/run.sh`
7. `combined/run.sh`
8. `RUN_ALL_MODE=1` 时调用 `service-sync/switch-mode.sh all`

首次清理后脚本会导出 `RESET_CASES=0`，避免后续用例互相删除资源。

### `service-sync/run.sh`

运行主要的 Kubernetes Service 同步验证：

```bash
cd integration/cases
RESET_CASES=1 ./service-sync/run.sh
```

覆盖内容包括同步开关及优先级、历史 `enableRegister` 注解、`all` 与 `demand`
模式、Service 和实例 metadata、协议与权重映射、TTL、StatefulSet 自定义权重、
NotReady Endpoints、别名、Label 收敛，以及关闭同步后的清理。

脚本会读取当前 `serviceSync.mode`。只有在 `demand` 模式下才会验证
`it-pc-sync-all` 中的 Service 未同步；全量模式用例请使用
`switch-mode.sh` 单独运行。

### `service-sync/alias-winner.sh`

验证多个 Service 指向同一别名时只保留一个稳定目标，以及目标 Service 删除后
别名是否切换到剩余 Service：

```bash
cd integration/cases
./service-sync/alias-winner.sh
```

脚本会自行应用 `service-sync/manifests.yaml`，并为每次运行生成唯一的
Service 和别名名称。临时 Service 删除后，生成的别名会保留在 Polaris 中。

### `service-sync/switch-mode.sh`

切换 `serviceSync.mode`，等待 controller rollout，验证
`it-pc-sync-all/all-mode-demo` 的 Service 和实例，然后恢复原模式：

```bash
cd integration/cases
./service-sync/run.sh
./service-sync/switch-mode.sh all
./service-sync/switch-mode.sh demand
```

参数格式：

```text
./service-sync/switch-mode.sh <all|demand>
```

运行前必须已经存在 `it-pc-sync-all` 工作负载，先执行
`service-sync/run.sh` 可以满足该条件。脚本会修改
`polaris-system/injector-mesh` 并重启
`statefulset/polaris-controller`。

### `lifecycle/namespace/run.sh`

验证 Namespace 的创建和删除语义：

```bash
cd integration/cases
RESET_CASES=1 ./lifecycle/namespace/run.sh
```

用例创建开启同步的 Namespace 和 Service，等待 Polaris 中对应对象出现；
删除 Kubernetes Namespace 后，断言 Polaris Namespace 和 Service 仍保留，
而由 controller 同步的实例被移除。Polaris Namespace 在该测试路径中不会被
删除，因此会在多次运行之间保留。

### `lifecycle/service/run.sh`

验证 Service 的创建、删除、类型覆盖和跨 Namespace 别名：

```bash
cd integration/cases
RESET_CASES=1 ./lifecycle/service/run.sh
```

用例覆盖 ClusterIP、NodePort 和 LoadBalancer 三种 Service，验证跨 Namespace
别名；删除 Service 前会手工注册一个 SDK 实例和一个其他集群的 controller
实例，用来证明删除只清理本集群 controller 管理的实例，同时保留 Polaris
Service。

ExternalName Service 没有 selector 和 ports，不在 controller 同步路径内，
因此不作为正向同步用例。

### `lifecycle/endpoints/run.sh`

验证 Endpoints 地址变化后的收敛：

```bash
cd integration/cases
RESET_CASES=1 ./lifecycle/endpoints/run.sh
```

用例将 Deployment 从 1 个副本扩到 2 个，再缩到 1 个和 0 个；逐步比较
Ready Endpoints 的 IP:port 集合与 Polaris 实例集合，验证 Ready 实例为健康且
未隔离，并确认地址全部移除后 Polaris Service 仍保留。

### `inject/run.sh`

运行 Sidecar 和 Java Agent 注入用例：

```bash
cd integration/cases
./inject/run.sh
```

覆盖 DNS Sidecar 注入及参数、Namespace 和 Pod Label 命中、显式关闭注解和
Label、hostNetwork 行为、Java Agent 参数与工作负载名称回退、历史 Namespace
Label 兼容，以及“只开启注入不会自动创建 Polaris Service”的边界。

### `combined/run.sh`

验证 Service 同步和注入可以同时工作：

```bash
cd integration/cases
./combined/run.sh
```

检查 `it-pc-combo/combo-demo` 的 Sidecar 注入、`sidecarConfig`、Service
metadata、实例 metadata 和实例数量。

### `cleanup.sh`

删除全部固定测试 Namespace，并等待删除完成：

```bash
cd integration/cases
./cleanup.sh
```

删除的 Namespace 集合与设置 `CLEANUP=1` 相同，并额外等待 Namespace 删除完成。
该脚本不会删除 Polaris 别名或 Polaris Namespace。测试后如果要求 Polaris
环境干净，需要自行检查并删除 `shared-alias`、`metadata-demo-alias`、
`shared-alias-<timestamp>-<pid>` 形式的生成别名，以及 Namespace 生命周期
用例保留的 Polaris Namespace。

### `lib.sh`

公共 Shell 函数，包括日志、命令检查、Namespace 清理、kubectl 等待、Polaris
HTTP 查询和断言函数。不要直接执行。

## 单独运行用例

所有用例都通过 `lib.sh` 解析相对路径，因此可以从任意工作目录启动：

```bash
/absolute/path/to/integration/cases/service-sync/run.sh
```

单个用例默认 `RESET_CASES=0`。需要干净环境时设置 `RESET_CASES=1`。注意当前
实现中，任意单用例入口设置 `RESET_CASES=1` 都会调用公共清理函数，可能删除
全部测试 Namespace，而不只是当前用例的 Namespace，因此仅应在可丢弃的集群中
使用。

同样，单个入口设置 `CLEANUP=1` 也会在退出时删除全部测试 Namespace。

## 固定 Namespace

| Namespace | 用途 |
| --- | --- |
| `it-pc-sync` | Service 同步和 metadata 断言 |
| `it-pc-sync-legacy` | 历史 `enableRegister` 断言 |
| `it-pc-sync-all` | `serviceSync.mode` 断言 |
| `it-pc-inject-default` | Sidecar 注入和关闭规则 |
| `it-pc-inject-podlabel` | Pod Label 命中 |
| `it-pc-inject-java` | Java Agent 注入 |
| `it-pc-inject-legacy` | 历史 Namespace Label |
| `it-pc-combo` | 同步与注入组合 |
| `it-pc-ns-lifecycle` | Namespace 生命周期 |
| `it-pc-svc-lifecycle` | Service 生命周期和别名源 |
| `it-pc-svc-alias` | 跨 Namespace 别名目标 |
| `it-pc-ep-lifecycle` | Endpoints 生命周期 |

`cleanup.sh` 会删除上述全部 Namespace。如果 Namespace 因 Kubernetes finalizer
暂时显示为 `Terminating`，脚本最多等待 `WAIT_TIMEOUT` 秒。

## 故障排查

重试前先收集 controller 和用例状态：

```bash
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system logs statefulset/polaris-controller --tail=200
kubectl get namespaces | grep '^it-pc-'
kubectl -n it-pc-sync get pods,services,endpoints
kubectl -n it-pc-inject-default get pods
```

常见问题：

- `required command not found`：安装缺失的命令，或通过 `KUBECTL`、`CURL`、
  `JQ` 指定路径。
- `cannot determine Polaris serverAddress from injector-mesh`：检查
  ConfigMap，或者显式设置 `POLARIS_HTTP_URL`。
- HTTP API 超时或连接失败：确认执行机可以访问 Polaris 的 8090 端口，或者
  提供正确的 `POLARIS_HTTP_URL` 和 `POLARIS_TOKEN`。
- Pod 或 rollout 等待超时：检查镜像拉取事件、节点资源和 readiness probe。
  `notready-demo` 用例会故意使用失败的 readiness probe，出现 NotReady
  Endpoint 是预期行为。
- Service 断言反复超时：检查 controller 到 Polaris 的连通性，并确认当前
  `serviceSync.mode` 与用例预期一致。
- Namespace 清理超时：检查残留 finalizer 和 Namespace 中尚未删除的资源，
  清理后重试。
- `switch-mode.sh` 在 `KEEP_MODE=1` 时中断：需要手动恢复
  `serviceSync.mode` 并重启 controller。未设置 `KEEP_MODE=1` 时，脚本的
  EXIT trap 会尝试恢复原模式。
