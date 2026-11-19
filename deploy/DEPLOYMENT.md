# Polaris Controller Deployment Guide

[简体中文](#简体中文) | [English](#english)

This repository provides two deployment paths:

- Helm, recommended for normal installation, upgrade, and rollback.
- Kubernetes YAML manifests, for environments that cannot use Helm.

Both paths use `deploy/deploy.sh` for validation and rendering. The legacy
scripts remain available as compatibility entry points.

---

## 简体中文

### 1. 部署方式

| 方式 | 适用场景 | 入口 |
| --- | --- | --- |
| Helm | 常规生产部署、升级、回滚 | `deploy/deploy.sh install helm` |
| Kubernetes YAML | 无法使用 Helm、需要直接审查清单 | `deploy/deploy.sh install manifests` |

推荐先渲染并检查清单，再执行实际部署。

### 2. 前置条件

通用要求：

- 已准备可用的 Kubernetes 集群和 kubeconfig。
- 已部署 Polaris Server。
- 默认 controller Namespace 为 `polaris-system`。当前 Chart 和原生清单均以该 Namespace 为部署目标。
- 当前提供 `v1.21` 和 `v1.22` 两套清单。Kubernetes `v1.21` 使用 `v1.21` 清单；`v1.22` 及以上版本统一复用 `v1.22` 清单，补丁版本和厂商后缀会自动归一化。
- Helm 默认 2 副本，可通过 `--set controller.replicas=N` 调整。StatefulSet 使用 `OrderedReady` 滚动更新，并配置 9443 端口 startup/readiness/liveness probe、preferred pod anti-affinity 和按 Pod 名隔离的日志目录。

方式要求：

- Helm：需要 `helm`。
- 原生清单：需要 `kubectl`。
- Helm 卸载和残留清理：还需要 `jq`。

### 3. 配置来源

Helm 默认读取：

```text
deploy/variables.txt
```

Kubernetes YAML 默认读取：

```text
deploy/kubernetes_v1.21/kubernetes/variables.txt
deploy/kubernetes_v1.22/kubernetes/variables.txt
```

配置优先级从低到高：

```text
variables.txt < PC_<KEY> 环境变量 < --set KEY=VALUE
```

支持以下变量：

| 变量 | 说明 |
| --- | --- |
| `POLARIS_HOST` | Polaris Server 地址 |
| `CONTROLLER_VERSION` | controller 及相关初始化镜像版本 |
| `SIDECAR_VERSION` | polaris-sidecar 镜像版本 |
| `POLARIS_TOKEN` | Polaris Server 鉴权 Token |
| `POLARIS_OPERATOR` | Polaris 主账户 ID |
| `ENVOY_VERSION` | Envoy 镜像版本 |
| `CLUSTER_NAME` | Kubernetes 集群名称和配置默认分组 |
| `JAVA_AGENT_INIT` | Java Agent 初始化镜像版本 |
| `SYNC_MODE` | Service 同步模式：`all` 或 `demand` |

敏感参数优先通过环境变量传入：

```bash
export PC_POLARIS_TOKEN='<polaris-token>'
export PC_POLARIS_OPERATOR='<polaris-operator>'
export PC_CONTROLLER_VERSION='<controller-version>'
```

`--set` 适合普通参数。Token 放入命令历史或进程参数前，应评估本机安全要求。

### 4. Helm 快速部署

先做 Helm 渲染检查：

```bash
cd /path/to/polaris-controller

PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --dry-run
```

安装或升级：

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --release polaris-controller \
  --namespace polaris-system
```

命令等价于幂等的 `helm upgrade --install`。可通过以下参数控制行为：

- 默认等待 workload 就绪。
- `--no-wait`：不等待 workload 就绪。
- `--timeout 10m`：设置 Helm 等待超时。
- `--set KEY=VALUE`：覆盖单个变量，可以重复使用。

只渲染 Helm values、不连接集群：

```bash
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh render helm \
  --kubernetes-version v1.22 \
  --output-dir ./generated/helm-values
```

输出文件：

```text
generated/helm-values/v1.22/values.yaml
```

渲染文件权限为 `0600`，源 `values.yaml` 不会被修改。

### 5. 原生 Kubernetes YAML 部署

先做客户端 dry-run：

```bash
cd /path/to/polaris-controller

PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22 \
  --dry-run
```

实际安装：

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22
```

只输出渲染后的清单：

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22 \
  --render-only ./generated/manifests
```

安装流程：

1. 在临时目录渲染全部 YAML。
2. 检查是否仍有未替换的占位符。
3. 应用 Namespace。
4. 从 `secrets/key.pem`、`cert.pem` 和 `ca-cert.pem` 创建 webhook TLS Secret。
5. 按依赖顺序应用 RBAC、配置、injector、metrics Service 和 controller。

临时渲染文件会在命令结束后删除。使用 `--render-only` 可以保留结果供审查。

### 6. 卸载

Helm 卸载：

```bash
./deploy/deploy.sh uninstall helm \
  polaris-controller polaris-system
```

脚本会：

1. 校验 release 名称、Namespace、revision 和 Chart。
2. 执行 `helm uninstall --wait`。
3. 确认 controller Pod 已退出。
4. 清理 controller sync-state ConfigMap。
5. 只删除经过 Helm 标签和 release 注解确认的残留 RBAC。

如果 release 已删除但残留清理中途失败：

```bash
./deploy/deploy.sh uninstall helm \
  polaris-controller polaris-system \
  --cleanup-residual
```

原生清单卸载：

```bash
./deploy/deploy.sh uninstall manifests \
  --kubernetes-version v1.22
```

默认顺序为：删除 webhook，删除 injector 和 controller，删除 Secret，等待 `k8s-app=polaris-controller` 的 controller Pod 全部消失（最多 120 秒），再清理 sync-state ConfigMap 和 RBAC。资源删除请求均使用 `--wait=false` 和 10 秒 API 超时，避免被 finalizer 无限阻塞；controller Pod 退出是唯一的对象级长等待。Helm 的 residual 清理会检查 `polaris-system` 中是否仍有任意带 `app=sidecar-injector` 的 controller Pod；只要存在，就保留共享 sync-state 账本。

如果 controller Pod 在 120 秒内仍未删除，脚本会输出警告，并执行：

```text
kubectl delete pods --force=true --grace-period=0 --wait=false --request-timeout=10s
```

强删请求在 10 秒内有界返回，即使失败也会继续删除状态 ConfigMap，避免被 Pod finalizer 无限阻塞。`polaris-system` Namespace 默认保留，因为其中可能同时部署 Polaris Server 或其他组件。

卸载日志中，`SUCCESS ... delete request accepted` 表示资源删除请求已被 Kubernetes API 接受；只有 `SUCCESS controller Pods: deleted within 120s` 表示 controller Pod 已确认删除。`WARN ... timed out` 表示请求超时，`WARN ... failed` 表示其他删除错误。

只有确认 Namespace 专属于该 controller 时，才使用：

```bash
./deploy/deploy.sh uninstall manifests \
  --kubernetes-version v1.22 \
  --delete-namespace
```

### 7. 兼容入口

原有脚本仍可继续使用：

```bash
./deploy/init_helm.sh --kubernetes-version v1.22
./deploy/uninstall_helm.sh polaris-controller polaris-system

cd deploy/kubernetes_v1.22/kubernetes
./install.sh --dry-run
./uninstall.sh
```

这些脚本现在只负责转发到 `deploy/deploy.sh`，不再各自复制部署逻辑。

### 8. 日志保留与安全

安装默认不额外等待。原生卸载会在清理状态账本前等待 controller Pod 退出，最多 120 秒。平台需要收集退出日志时可以设置：

```bash
LOG_HOLD_SECONDS=300 ./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --set CONTROLLER_VERSION='<controller-version>'
```

安全注意事项：

- 不要提交生成的 values 文件。
- 包含 Token 的变量文件权限建议设为 `0600`。
- 脚本不会打印变量值。
- `deploy/variables.txt` 中的 `##VERSION##` 是发布构建标记。直接安装时必须通过 `PC_CONTROLLER_VERSION` 或 `--set CONTROLLER_VERSION=...` 设置真实版本。

---

## English

### 1. Deployment methods

| Method | Use case | Entry point |
| --- | --- | --- |
| Helm | Production installs, upgrades, and rollback workflows | `deploy/deploy.sh install helm` |
| Kubernetes YAML | Environments without Helm and manifest review workflows | `deploy/deploy.sh install manifests` |

Render or dry-run the configuration before changing a cluster.

### 2. Prerequisites

General requirements:

- A working Kubernetes cluster and kubeconfig.
- A reachable Polaris Server.
- The default controller namespace is `polaris-system`. The current Helm charts and raw manifests target that namespace.
- Two manifest families are available. Kubernetes `v1.21` uses the `v1.21` manifests; Kubernetes `v1.22` and later reuse the `v1.22` manifests. Patch versions and vendor suffixes are normalized automatically.
- Helm defaults to two replicas and supports `--set controller.replicas=N`. The StatefulSet uses `OrderedReady` rolling updates with 9443 startup/readiness/liveness probes, preferred pod anti-affinity, and Pod-name-isolated log directories.

Tool requirements:

- Helm installation: `helm`.
- YAML installation: `kubectl`.
- Helm uninstall and residual cleanup: `jq`.

### 3. Configuration

Helm reads `deploy/variables.txt` by default.

Raw Kubernetes YAML reads the `variables.txt` file in the selected version directory.

Precedence, from lowest to highest:

```text
variables.txt < PC_<KEY> environment variables < --set KEY=VALUE
```

Supported variables:

| Variable | Meaning |
| --- | --- |
| `POLARIS_HOST` | Polaris Server address |
| `CONTROLLER_VERSION` | Controller and related init image version |
| `SIDECAR_VERSION` | polaris-sidecar image version |
| `POLARIS_TOKEN` | Polaris Server authentication token |
| `POLARIS_OPERATOR` | Polaris operator account ID |
| `ENVOY_VERSION` | Envoy image version |
| `CLUSTER_NAME` | Kubernetes cluster identity and default config group |
| `JAVA_AGENT_INIT` | Java Agent init image version |
| `SYNC_MODE` | Service sync mode: `all` or `demand` |

Sensitive values can be supplied through environment variables:

```bash
export PC_POLARIS_TOKEN='<polaris-token>'
export PC_POLARIS_OPERATOR='<polaris-operator>'
export PC_CONTROLLER_VERSION='<controller-version>'
```

### 4. Helm quick start

Render with Helm first:

```bash
cd /path/to/polaris-controller

PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --dry-run
```

Install or upgrade:

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --release polaris-controller \
  --namespace polaris-system
```

The command uses idempotent `helm upgrade --install`.

Render values without contacting a cluster:

```bash
PC_CONTROLLER_VERSION='<controller-version>' \
./deploy/deploy.sh render helm \
  --kubernetes-version v1.22 \
  --output-dir ./generated/helm-values
```

The output is written to `generated/helm-values/v1.22/values.yaml` with mode `0600`. The source template is not modified.

### 5. Raw Kubernetes YAML

Client dry-run:

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22 \
  --dry-run
```

Install:

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22
```

Render only:

```bash
PC_POLARIS_TOKEN='<polaris-token>' \
PC_POLARIS_OPERATOR='<polaris-operator>' \
./deploy/deploy.sh install manifests \
  --kubernetes-version v1.22 \
  --render-only ./generated/manifests
```

The installer renders YAML into a temporary directory, validates placeholders, creates the webhook TLS Secret, and applies resources in dependency order.

### 6. Uninstall

Helm:

```bash
./deploy/deploy.sh uninstall helm \
  polaris-controller polaris-system
```

The script verifies release ownership before calling `helm uninstall --wait`. Runtime state is deleted only when no controller Pods labelled `app=sidecar-injector` remain in `polaris-system`. It then removes controller sync-state ConfigMaps and only those residual ClusterRoles and ClusterRoleBindings with matching Helm release annotations and labels.

Retry residual cleanup:

```bash
./deploy/deploy.sh uninstall helm \
  polaris-controller polaris-system \
  --cleanup-residual
```

Raw manifests:

```bash
./deploy/deploy.sh uninstall manifests \
  --kubernetes-version v1.22
```

The webhook configuration is removed first, followed by the injector and controller. Resource deletion requests use `--wait=false` and a 10-second API timeout, so finalizers cannot block those commands indefinitely. The script then waits up to 120 seconds for controller Pods with `k8s-app=polaris-controller` to disappear before deleting sync-state ConfigMaps and RBAC. If the Pods are still present after the timeout, the script logs a warning and force-deletes them with a bounded request, then continues.

`SUCCESS ... delete request accepted` means the Kubernetes API accepted the delete request. Only `SUCCESS controller Pods: deleted within 120s` confirms that the controller Pods are gone. `WARN ... timed out` and `WARN ... failed` distinguish request timeout from other deletion errors.

The `polaris-system` Namespace is preserved by default because it may host Polaris Server or other resources.

Delete the Namespace only when it is dedicated to this controller:

```bash
./deploy/deploy.sh uninstall manifests \
  --kubernetes-version v1.22 \
  --delete-namespace
```

### 7. Compatibility entry points

The original scripts remain available:

```bash
./deploy/init_helm.sh --kubernetes-version v1.22
./deploy/uninstall_helm.sh polaris-controller polaris-system

cd deploy/kubernetes_v1.22/kubernetes
./install.sh --dry-run
./uninstall.sh
```

They delegate to `deploy/deploy.sh` so validation and rendering behavior stay consistent.

### 8. Logs and security

Install and uninstall do not add an artificial delay by default. Use the following when an external platform needs to collect exit logs:

```bash
LOG_HOLD_SECONDS=300 ./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --set CONTROLLER_VERSION='<controller-version>'
```

Do not commit generated values files. Restrict files containing tokens to mode `0600`. The scripts do not print variable values.

`##VERSION##` in `deploy/variables.txt` is a release-build marker. When installing directly from source, provide a real image tag through `PC_CONTROLLER_VERSION` or `--set CONTROLLER_VERSION=...`.
