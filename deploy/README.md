# Polaris Controller deployment

English | [简体中文](./README-zh.md) | [Bilingual deployment guide](./DEPLOYMENT.md)

The complete bilingual guide is available in [DEPLOYMENT.md](./DEPLOYMENT.md).

Kubernetes `v1.21` uses the v1.21 manifests. Kubernetes `v1.22` and later reuse the v1.22 manifests.

Recommended Helm quick start:

```bash
PC_POLARIS_TOKEN='<token>' \
PC_POLARIS_OPERATOR='<operator>' \
PC_CONTROLLER_VERSION='<version>' \
./deploy/deploy.sh install helm --kubernetes-version v1.22 --dry-run
```

After the render is reviewed:

```bash
PC_POLARIS_TOKEN='<token>' \
PC_POLARIS_OPERATOR='<operator>' \
PC_CONTROLLER_VERSION='<version>' \
./deploy/deploy.sh install helm \
  --kubernetes-version v1.22 \
  --release polaris-controller \
  --namespace polaris-system
```

See [DEPLOYMENT.md](./DEPLOYMENT.md) for variable precedence, raw Kubernetes
manifests, dry-run and render-only workflows, safe uninstall, residual cleanup,
compatibility entry points, and security notes.
