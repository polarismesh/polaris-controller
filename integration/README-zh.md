# Polaris Controller 集成测试

本目录用于在真实 Kubernetes 集群和已部署的 Polaris 上验证 Polaris
Controller。测试会创建真实资源并查询 Polaris HTTP API，不使用模拟后端。

相关文档：

- [中文用例说明](cases/test-zh.md)
- [English test cases](cases/test.md)
- [Polaris Controller 用户指南](../docs/README.md)

## 使用警告

- 测试使用固定 Namespace，完整运行默认先删除旧测试 Namespace。
- 不要在共享集群中复用这些 Namespace。
- 生命周期用例会主动删除或缩容对象；“保留资源”不会重建被用例删除的对象。
- 本地通过不代表 CI 或正式发布验收通过。

## 目录结构

```text
integration/
├── README.md / README-zh.md
├── build-materials.sh             # 生成人工上传的 zip
├── run.sh                         # 执行并落盘日志
├── cleanup.sh                     # 清理测试资源
├── pack-logs.sh                   # 汇总日志和集群快照
└── cases/
    ├── test.md / test-zh.md       # 中英文用例说明
    ├── run-all.sh                 # 完整测试入口
    ├── inspect.sh                 # 查看保留资源
    ├── cleanup.sh                 # 清理 Namespace
    ├── service-sync/              # Service 同步及可选模式用例
    ├── lifecycle/                 # Namespace/Service/Endpoints 生命周期
    ├── inject/                    # Sidecar/Java Agent 注入
    └── combined/                  # 同步和注入组合
```

## 前置条件

1. Controller 已部署在 `polaris-system`，StatefulSet 名为
   `polaris-controller`。
2. `polaris-system/injector-mesh` 存在，并包含 `serverAddress`，需要鉴权时
   包含 `accessToken`。
3. 当前 kubeconfig 有创建、修改和删除测试资源的权限；全模式用例还需要修改
   ConfigMap 并重启 controller。
4. 执行机可以访问 Polaris HTTP API，默认地址为
   `http://<serverAddress>:8090`。
5. Polaris Token 可以查询 Namespace、Service、Instance 和 Alias，并能注册
   和删除生命周期用例的测试实例。
6. 已安装 `bash`、`kubectl`、`curl` 和 `jq`。
7. 集群可以拉取 `ccr.ccs.tencentyun.com` 上的测试镜像。
8. 集群允许创建 Service 生命周期用例需要的 NodePort 和 LoadBalancer。

快速检查：

```bash
kubectl config current-context
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system get configmap injector-mesh
command -v bash kubectl curl jq
```

`KUBECTL`、`CURL`、`JQ` 只接受可执行文件名或路径，不接受附加参数。

## 快速开始

```bash
cd integration/cases
./run-all.sh
```

默认流程：

1. 等待 controller rollout。
2. 删除旧测试 Namespace。
3. 运行 Service 同步、生命周期、注入和组合套件。
4. 保留运行结束时仍然存在的测试资源。
5. 打印保留的 Namespace、巡检命令和清理命令。

可选用例：

```bash
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run-all.sh --keep
```

`RUN_ALL_MODE=1` 会临时切换 `serviceSync.mode=all`、重启 controller，并在
结束后恢复原模式。

## 资源保留与清理

```bash
# 人工检查
./inspect.sh

# 检查完成后清理
./cleanup.sh

# 或者运行结束时立即清理
./run-all.sh --cleanup
```

`KEEP_RESOURCES=1` 是 `run-all.sh` 的默认值，与 `CLEANUP=1` 互斥。
`CLEANUP=1 ./run-all.sh` 仍兼容。每次运行默认执行 `RESET_CASES=1`，因此再次
运行前需要确认是否要保留上一轮资源。

## 打包、上传与执行

### 生成 zip

```bash
cd integration
./build-materials.sh
```

默认输出：

```text
integration/dist/polaris-controller-integration.zip
```

zip 包含可直接在测试机执行的脚本和完整用例：

```text
polaris-controller-integration/
├── run.sh
├── cleanup.sh
├── pack-logs.sh
├── PACKAGE-INFO.txt
├── integration/
└── docs/README.md
```

使用 `--output PATH` 指定 zip 路径，使用 `--clean` 删除 `integration/dist/`。

### 人工上传和执行

将 zip 人工上传到已配置 kubeconfig 的云端测试机，例如：

```bash
scp integration/dist/polaris-controller-integration.zip <user>@<host>:/tmp/
```

在测试机解压并执行：

```bash
cd /tmp
unzip polaris-controller-integration.zip
cd polaris-controller-integration

./run.sh
```

`run.sh` 会把终端输出同时写入 `.logs/run-<timestamp>-<pid>/run.log`，并保持
测试进程的退出码。其他参数会传给 `integration/cases/run-all.sh`：

```bash
./run.sh --keep
RUN_ALIAS_WINNER=1 RUN_ALL_MODE=1 ./run.sh --keep
./run.sh --log-dir /tmp/polaris-logs --keep
```

默认保留 Kubernetes 测试资源，便于人工检查。

### 打包日志并回传

```bash
./pack-logs.sh
```

默认归档包位于当前解压目录：

```text
polaris-controller-integration-logs-<timestamp>.zip
```

如果远端测试机没有 `zip` 命令，脚本会改用 `.tar.gz`。

归档包含 `.logs/` 下的运行日志，并默认收集：

- 当前 kubectl context；
- `polaris-controller` 最近 2000 行日志；
- 仍存在的测试 Namespace、Pod、工作负载、Service 和 Endpoints；
- Namespace Events 和 Pod describe。

使用 `--no-cluster` 或 `COLLECT_CLUSTER=0` 可只打包已有日志：

```bash
./pack-logs.sh --no-cluster
```

脚本会打印 `scp` 示例，把归档包回传到本地。归档可能包含集群元数据，分享前
请先检查内容。

### 清理资源

人工检查完成后执行：

```bash
./cleanup.sh
```

清理范围是 Kubernetes 测试 Namespace，不删除 Polaris 别名、Polaris Namespace
或仍保留的 Polaris Service。清理不会删除 `.logs/` 和日志归档。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `POLARIS_HTTP_URL` | 自动推导 | Polaris HTTP API 地址。 |
| `POLARIS_TOKEN` | 从 ConfigMap 读取 | Polaris API Token。 |
| `KUBECTL` / `CURL` / `JQ` | 命令名 | 覆盖工具路径。 |
| `WAIT_TIMEOUT` | `120` | rollout、Pod、Namespace 等待时间，单位秒。 |
| `RESYNC_TIMEOUT` | `180` | Polaris 对账和断言等待时间，单位秒。 |
| `EXTERNAL_DELETE_TIMEOUT` | `30` | 外部实例清理等待时间，单位秒。 |
| `RESET_CASES` | `run-all.sh` 为 `1` | 运行前删除固定测试 Namespace。 |
| `KEEP_RESOURCES` | `1` | 退出时保留测试资源，仅 `run-all.sh` 使用。 |
| `CLEANUP` | `0` | 退出时删除测试 Namespace，与保留模式互斥。 |
| `RUN_ALIAS_WINNER` | `0` | 运行别名 winner/handover 用例。 |
| `RUN_ALL_MODE` | `0` | 运行 `serviceSync.mode=all` 用例并恢复原模式。 |
| `KEEP_MODE` | `0` | 调试用，不恢复 `serviceSync.mode`。 |
| `LOG_DIR` | `./.logs` | `run.sh` 和 `pack-logs.sh` 使用的日志目录。 |
| `COLLECT_CLUSTER` | `1` | `pack-logs.sh` 是否收集 Kubernetes 和 controller 快照。 |

示例：

```bash
POLARIS_HTTP_URL="http://10.0.0.10:8090" \
POLARIS_TOKEN="<polaris-token>" \
WAIT_TIMEOUT=180 \
RESYNC_TIMEOUT=300 \
./run-all.sh --keep
```

manifest 中的 Namespace 名称是固定的。不要只覆盖 `NS_*` 环境变量而不修改
manifest，否则断言和实际资源会不一致。

## 执行入口

| 入口 | 用例 |
|---|---|
| `service-sync/run.sh` | `SYNC-01` 至 `SYNC-14` |
| `service-sync/alias-winner.sh` | `ALIAS-01` |
| `service-sync/switch-mode.sh` | `MODE-01` |
| `lifecycle/namespace/run.sh` | `NS-LIFE-01` 至 `NS-LIFE-02` |
| `lifecycle/service/run.sh` | `SVC-LIFE-01` 至 `SVC-LIFE-02` |
| `lifecycle/endpoints/run.sh` | `EP-LIFE-01` 至 `EP-LIFE-04` |
| `inject/run.sh` | `INJ-01` 至 `INJ-11` |
| `combined/run.sh` | `COMBO-01` |
| `cleanup.sh` | `CLEAN-01` |
| `run-all.sh` | `ORCH-01` |
| 包根目录 `run.sh` | 执行并记录日志 |
| 包根目录 `cleanup.sh` | 清理测试资源 |
| 包根目录 `pack-logs.sh` | 打包日志和集群快照 |

单个入口默认 `RESET_CASES=0`。设置 `RESET_CASES=1` 会删除全部测试
Namespace，而不只是当前套件的 Namespace。完整预期结果和验证方式见
[中文用例说明](cases/test-zh.md)。

## 测试 Namespace

```text
it-pc-sync
it-pc-sync-legacy
it-pc-sync-all
it-pc-inject-default
it-pc-inject-podlabel
it-pc-inject-java
it-pc-inject-legacy
it-pc-inject-no-mode
it-pc-combo
it-pc-ns-lifecycle
it-pc-svc-lifecycle
it-pc-svc-alias
it-pc-ep-lifecycle
```

`cleanup.sh` 删除这些 Kubernetes Namespace，但不会删除 Polaris 别名、
Polaris Namespace 或仍保留的 Polaris Service。

## 故障排查

```bash
kubectl -n polaris-system get statefulset polaris-controller
kubectl -n polaris-system logs statefulset/polaris-controller --tail=200
kubectl get namespaces | grep '^it-pc-'
kubectl -n it-pc-sync get pods,services,endpoints
```

- 缺少命令：安装对应工具，或设置 `KUBECTL`、`CURL`、`JQ`。
- 无法读取 `serverAddress`：检查 `injector-mesh`，或设置
  `POLARIS_HTTP_URL`。
- HTTP API 超时：检查执行机到 Polaris 的网络、端口和 Token。
- Pod 或 rollout 超时：检查镜像拉取、节点资源和 readiness probe。
- Service 收敛超时：检查 controller 到 Polaris 的连通性及当前同步模式。
- Namespace 长时间 Terminating：检查 finalizer 和残留 Pod。
- `switch-mode.sh` 在 `KEEP_MODE=1` 时中断：手动恢复 `serviceSync.mode`
  并重启 controller。
