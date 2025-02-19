package inject

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/hashicorp/go-multierror"

	"github.com/polarismesh/polaris-controller/common/log"
)

// getPodPatch 处理 admission webhook 请求
// 返回值:
//
//	patchBytes: []byte - 包含需要应用到资源上的 JSON patch 数据。如果不需要修改，则为 nil
//	err: error - 处理过程中遇到的错误
//	  - nil: 表示处理成功
//	  - non-nil: 表示处理过程中出现错误，错误信息包含在 error 中
func (wh *Webhook) getPodPatch(p *podDataInfo) ([]byte, error) {
	// 检查Pod元数据是否合法
	passed, err := p.checkPodData()
	if err != nil || !passed {
		log.InjectScope().Errorf("[Webhook] skip due to checkPodData result:%v, error: %v", passed, err)
		return nil, err
	}

	// 检查Pod是否需要注入,过滤掉某些不需要注入的pod
	if !wh.requireInject(p) {
		skipReason := fmt.Sprintf("policy check failed for namespace=%s, podName=%s, injectConfig=%v",
			p.podObject.Namespace, p.podName, p.injectTemplateConfig)
		log.InjectScope().Infof("Skipping due to: %s", skipReason)
		return nil, nil
	}

	// 获取注入后的annotations
	sidecarTemplate := p.injectTemplateConfig.Template
	values := map[string]interface{}{}
	valuesConfig := wh.templateConfig.GetValuesConfig()
	if err := yaml.Unmarshal([]byte(wh.templateConfig.GetValuesConfig()), &values); err != nil {
		log.InjectScope().Infof("[Webhook] failed to parse values config: %v [%v]\n", err, valuesConfig)
		return nil, multierror.Prefix(err, "could not parse configuration values:")
	}
	metadataCopy := p.podObject.ObjectMeta.DeepCopy()
	metadataCopy.Annotations = p.injectedAnnotations
	templateData := SidecarTemplateData{
		TypeMeta:       p.workloadType,
		DeploymentMeta: p.workloadMeta,
		ObjectMeta:     metadataCopy,
		Spec:           &p.podObject.Spec,
		ProxyConfig:    wh.templateConfig.GetMeshEnvoyConfig().GetDefaultConfig(),
		Values:         values,
	}

	funcMap := template.FuncMap{
		"formatDuration":      formatDuration,
		"isset":               isset,
		"excludeInboundPort":  excludeInboundPort,
		"includeInboundPorts": includeInboundPorts,
		"kubevirtInterfaces":  kubevirtInterfaces,
		"applicationPorts":    applicationPorts,
		"annotation":          getAnnotation,
		"valueOrDefault":      valueOrDefault,
		"toJSON":              toJSON,
		"toJson":              toJSON, // Used by, e.g. Istio 1.0.5 template sidecar-injector-configmap.yaml
		"fromJSON":            fromJSON,
		"structToJSON":        structToJSON,
		"protoToJSON":         protoToJSON,
		"toYaml":              toYaml,
		"indent":              indent,
		"directory":           directory,
		"contains":            flippedContains,
		"toLower":             strings.ToLower,
		"openTlsMode":         openTlsMode,
		"env":                 env,
		"render":              render,
	}

	bbuf, err := parseTemplate(sidecarTemplate, funcMap, templateData)
	if err != nil {
		return nil, err
	}

	var injectData SidecarInjectionSpec
	if err := yaml.Unmarshal(bbuf.Bytes(), &injectData); err != nil {
		// This usually means an invalid injector template; we can't check
		// the template itself because it is merely a string.
		log.InjectScope().Warnf("Failed to unmarshal template %v \n%s", err, bbuf.String())
		return nil, multierror.Prefix(err, "failed parsing injected YAML (check sidecar injector configuration):")
	}

	// set sidecar --concurrency
	applyConcurrency(injectData.Containers)
	injectStatus := p.addInjectStatusAnnotation(injectData)
	// 生成POD修改的patch
	opt := &PatchOptions{
		Pod:          p.podObject,
		KubeClient:   wh.k8sClient,
		PrevStatus:   injectionStatus(p.podObject),
		SidecarMode:  p.injectMode,
		WorkloadName: p.workloadMeta.Name,
		Sic:          &injectData,
		Annotations:  p.injectedAnnotations,
		ExternalInfo: map[string]string{},
	}
	patchBytes, err := createPatch(opt)
	if err != nil {
		log.InjectScope().Errorf(fmt.Sprintf("AdmissionResponse: err=%v injectStatus:%s injectData=%v\n", err,
			injectStatus, injectData))
		return nil, err
	}
	return patchBytes, err
}
