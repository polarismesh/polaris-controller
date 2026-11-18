# Polaris Controller 部署文档

简体中文 | [English](./README.md) | [中英双语部署指南](./DEPLOYMENT.md)

完整的中英文说明见 [DEPLOYMENT.md](./DEPLOYMENT.md)。

推荐的 Helm 快速检查：

```bash
PC_POLARIS_TOKEN='<token>' \
PC_POLARIS_OPERATOR='<operator>' \
PC_CONTROLLER_VERSION='<version>' \
./deploy/deploy.sh install helm --kubernetes-version v1.22 --dry-run
```

检查通过后执行安装或升级：

```bash
PC_POLARIS_TOKEN='<token>' \
PC_POLARIS_OPERATOR='<operator>' \
PC_CONTROLLER_VERSION='<version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --release polaris-controller \
  --namespace polaris-system
```

[DEPLOYMENT.md](./DEPLOYMENT.md) 包含配置优先级、原生 Kubernetes YAML 部署、
dry-run 和只渲染模式、安全卸载与残留清理、旧脚本兼容入口以及安全注意事项。
