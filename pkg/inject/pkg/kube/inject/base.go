package inject

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/ghodss/yaml"
	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
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
		log.InjectScope().Errorf(fmt.Sprintf("AdmissionResponse: err=%v injectData=%v\n", err, injectData))
		return nil, err
	}
	return patchBytes, err
}

func (wh *Webhook) requireInject(p *podDataInfo) bool {
	switch p.injectMode {
	case utils.SidecarForMesh:
		return wh.meshInjectRequired(p)
	case utils.SidecarForJavaAgent, utils.SidecarForDns:
		return wh.commonInjectRequired(p)
	default:
		return false
	}
}

// 是否需要注入, 规则参考istio官方定义
// https://istio.io/latest/zh/docs/ops/common-problems/injection/
func (wh *Webhook) commonInjectRequired(p *podDataInfo) bool {
	pod := p.podObject
	templateConfig := p.injectTemplateConfig

	if isIgnoredNamespace(pod.Namespace) {
		return false
	}

	// annotations为最高优先级
	injectionRequested, useDefaultPolicy := parseInjectionAnnotation(pod.GetAnnotations())
	log.InjectScope().Infof("parseInjectionAnnotation for %s/%s: UseDefault=%v injectionRequested=%v",
		pod.Namespace, pod.Name, useDefaultPolicy, injectionRequested)

	// Labels为次优先级
	if useDefaultPolicy {
		injectionRequested, useDefaultPolicy = evaluateSelectors(pod, templateConfig, p.podName)
		log.InjectScope().Infof("evaluateSelectors for %s/%s: UseDefault=%v injectionRequested=%v",
			pod.Namespace, pod.Name, useDefaultPolicy, injectionRequested)
	}

	// Policy为最低优先级
	required := determineInjectionRequirement(templateConfig.Policy, useDefaultPolicy, injectionRequested)
	log.InjectScope().Infof("determineInjectionRequirement for %s/%s: Policy=%s UseDefault=%v Requested=%v Required=%v",
		pod.Namespace, pod.Name, templateConfig.Policy, useDefaultPolicy, injectionRequested, required)

	return required
}

func isIgnoredNamespace(namespace string) bool {
	for _, ignored := range ignoredNamespaces {
		if namespace == ignored {
			return true
		}
	}
	return false
}

// 解析Annotation, 用于注解位置的黑白名单功能
func parseInjectionAnnotation(annotations map[string]string) (requested bool, useDefault bool) {
	var value string
	newFlag, newExists := annotations[utils.PolarisInjectionKey]
	if newExists {
		value = strings.ToLower(newFlag)
	} else {
		if flag, ok := annotations[annotation.SidecarInject.Name]; ok {
			value = strings.ToLower(flag)
		}
	}
	switch value {
	case "y", "yes", "true", "on", "enable", "enabled":
		// annotation白名单, 显示开启
		return true, false
	case "n", "no", "false", "off", "disable", "disabled":
		// annotation黑名单, 显示关闭
		return false, false
	default: // including empty string
		return false, true
	}
}

// 检查label的黑白名单功能
func evaluateSelectors(pod *corev1.Pod, config *config.TemplateConfig, podName string) (bool, bool) {
	// Check NeverInject selectors first
	if inject, ok := checkSelectors(pod, config.NeverInjectSelector, "NeverInjectSelector", podName, false); ok {
		return inject, false
	}

	// Then check AlwaysInject selectors
	if inject, ok := checkSelectors(pod, config.AlwaysInjectSelector, "AlwaysInjectSelector", podName, true); ok {
		return inject, false
	}

	return false, true
}

func checkSelectors(pod *corev1.Pod, selectors []metav1.LabelSelector, selectorType string, podName string,
	allowInject bool) (bool, bool) {
	for _, selector := range selectors {
		ls, err := metav1.LabelSelectorAsSelector(&selector)
		if err != nil {
			log.InjectScope().Warnf("Invalid %s: %v (%v)", selectorType, selector, err)
			continue // Continue checking other selectors
		}

		if !ls.Empty() && ls.Matches(labels.Set(pod.Labels)) {
			log.InjectScope().Infof("Pod %s/%s: %s matched labels %v",
				pod.Namespace, podName, selectorType, pod.Labels)
			return allowInject, true
		}
	}
	return false, false
}

func determineInjectionRequirement(policy config.InjectionPolicy, useDefault bool, requested bool) bool {
	switch policy {
	case config.InjectionPolicyEnabled:
		if useDefault {
			return true
		}
		return requested
	case config.InjectionPolicyDisabled:
		if useDefault {
			return false
		}
		return requested
	default:
		log.InjectScope().Errorf("Invalid injection policy: %s. Valid values: [%s, %s]",
			policy, config.InjectionPolicyDisabled, config.InjectionPolicyEnabled)
		return false
	}
}
