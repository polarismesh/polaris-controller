# Envoy bootstrap 配置文件产生容器

本容器作为 Envoy Sidecar 的 Init Container 在 Envoy 启动之前运行，主要功能是为 Envoy 产生初始化配置。

您可以在腾讯云的公共镜像仓库 ccr.ccs.tencentyun.com/polaris_mesh/polaris-sidecar-bootstrap-generator 中找到这个容器。