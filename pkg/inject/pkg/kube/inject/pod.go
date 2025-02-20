package inject

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

type podDataInfo struct {
	podObject             *corev1.Pod
	podName               string
	injectMode            utils.SidecarMode
	injectTemplateConfig  *config.TemplateConfig
	injectTemplateVersion string
	valuesConfig          string
	workloadMeta          *metav1.ObjectMeta
	workloadType          *metav1.TypeMeta
	injectedAnnotations   map[string]string
}

// 解析并填充 pod 的信息
func assignPodDataInfo(raw []byte, namespace string, wh *Webhook) (*podDataInfo, error) {
	var pod corev1.Pod
	if err := json.Unmarshal(raw, &pod); err != nil {
		handleError(fmt.Sprintf("Could not unmarshal raw object: %v %s", err,
			string(raw)))
		return nil, err
	}
	if pod.Namespace == "" {
		pod.Namespace = namespace
	}
	podInfo := &podDataInfo{
		podObject: &pod,
	}
	podInfo.assignAll(wh)
	return podInfo, nil
}

// 初始化podDataInfo所有字段
func (p *podDataInfo) assignAll(wh *Webhook) {
	p.assignPodName()
	p.assignInjectMode(wh)
	p.assignInjectTemplateAndVersion(wh)
	p.assignWorkloadMeta()
	p.assignInjectAnnotations()
}

func (p *podDataInfo) assignWorkloadMeta() {
	p.workloadMeta, p.workloadType = getOwnerMetaAndType(p.podObject)
}

// 通过Name或GenerateName获取Pod的名称
func (p *podDataInfo) assignPodName() {
	metadata := p.podObject.ObjectMeta
	if metadata.Name != "" {
		p.podName = metadata.Name
	} else if metadata.GenerateName != "" {
		p.podName = metadata.GenerateName + "***** (actual name not yet known)"
	}
}

// assignInjectTemplateAndVersion 获取注入配置
func (p *podDataInfo) assignInjectTemplateAndVersion(wh *Webhook) {
	switch p.injectMode {
	case utils.SidecarForJavaAgent:
		p.injectTemplateConfig, p.injectTemplateVersion = wh.templateConfig.GetSidecarJavaAgentTemplateAndVersion()
	case utils.SidecarForDns:
		p.injectTemplateConfig, p.injectTemplateVersion = wh.templateConfig.GetSidecarDnsTemplateAndVersion()
	default:
		// 向前兼容, 默认是mesh模式
		p.injectTemplateConfig, p.injectTemplateVersion = wh.templateConfig.GetSidecarMeshTemplateAndVersion()
	}
	log.InjectScope().Infof("podName=%s, injectMode=%s, injectTemplateConfig=%v", p.podName,
		utils.ParseSidecarModeName(p.injectMode), p.injectTemplateConfig)
	p.valuesConfig = wh.templateConfig.GetValuesConfig()
	return
}

// assignInjectMode 获取注入类型
func (p *podDataInfo) assignInjectMode(wh *Webhook) {
	p.injectMode = getInjectMode(wh, p)
}

// 检查pod的spec和annotations是否符合要求
func (p *podDataInfo) checkPodData() (bool, error) {
	pod := p.podObject
	if err := validateAnnotations(p.injectMode, pod.GetAnnotations()); err != nil {
		log.InjectScope().Errorf("[Webhook] injection failed due to invalid annotations: %v", err)
		return false, err
	}
	if p.injectMode == utils.SidecarForMesh {
		// If DNSPolicy is not ClusterFirst, the Envoy sidecar may not able to connect to polaris.
		if pod.Spec.DNSPolicy != "" && pod.Spec.DNSPolicy != corev1.DNSClusterFirst {
			log.InjectScope().Errorf("[Webhook] %q's DNSPolicy is not %q. The Envoy sidecar may not able to "+
				" connect to PolarisMesh Control Plane",
				pod.Namespace+"/"+p.podName, corev1.DNSClusterFirst)
			return false, nil
		}
	}
	return true, nil
}

// 处理pod的annotations注入
func (p *podDataInfo) assignInjectAnnotations() {
	md := p.podObject.ObjectMeta
	injectAnnotations := map[string]string{}
	if p.injectMode == utils.SidecarForMesh {
		// 设置需要注入到 envoy 的 md
		// 强制开启 XDS On-Demand 能力
		envoyMetadata := map[string]string{}
		// 这里负责将需要额外塞入的 annonation 数据进行返回
		if len(md.Annotations) != 0 {
			tlsMode := utils.MTLSModeNone
			if mode, ok := md.Annotations[utils.PolarisTLSMode]; ok {
				mode = strings.ToLower(mode)
				if mode == utils.MTLSModeStrict || mode == utils.MTLSModePermissive {
					tlsMode = mode
				}
			}
			injectAnnotations[utils.PolarisTLSMode] = tlsMode
			envoyMetadata[utils.PolarisTLSMode] = tlsMode

			// 按需加载能力需要显示开启
			if val, ok := md.Annotations[utils.SidecarEnvoyInjectKey]; ok {
				envoyMetadata[utils.SidecarEnvoyInjectKey] = val
			}
		}
		// 这里需要将 sidecar 所属的服务信息注入到 annonation 中，方便下发到 envoy 的 bootstrap.yaml 中
		// 命名空间可以不太强要求用户设置，大部份场景都是保持和 kubernetes 部署所在的 namespace 保持一致的
		if _, ok := md.Annotations[utils.SidecarNamespaceName]; !ok {
			injectAnnotations[utils.SidecarNamespaceName] = md.Namespace
			envoyMetadata[utils.SidecarNamespaceName] = md.Namespace
		}
		if svcName, ok := md.Annotations[utils.SidecarServiceName]; !ok {
			// 如果官方注解没有查询到，那就默认按照 istio 的约定，从 labels 中读取 app 这个标签的 value 作为服务名
			if val, ok := md.Labels["app"]; ok {
				injectAnnotations[utils.SidecarServiceName] = val
				envoyMetadata[utils.SidecarServiceName] = val
			} else {
				injectAnnotations[utils.SidecarServiceName] = p.workloadMeta.Name
				envoyMetadata[utils.SidecarServiceName] = p.workloadMeta.Name
			}
		} else {
			envoyMetadata[utils.SidecarServiceName] = svcName
		}

		for k, v := range md.Labels {
			envoyMetadata[k] = v
		}
		injectAnnotations[utils.SidecarEnvoyMetadata] = fmt.Sprintf("%q", toJSON(envoyMetadata))
	} else {
		// 注入workload的namespace和name
		if _, ok := md.Annotations[utils.SidecarNamespaceName]; !ok {
			injectAnnotations[utils.SidecarNamespaceName] = md.Namespace
		}
		if _, ok := md.Annotations[utils.SidecarServiceName]; !ok {
			injectAnnotations[utils.SidecarServiceName] = p.workloadMeta.Name
		}
	}
	p.injectedAnnotations = injectAnnotations
}

// SidecarInjectionStatus contains basic information about the
// injected sidecar. This includes the names of added containers and
// volumes.
type SidecarInjectionStatus struct {
	Version          string                        `json:"version"`
	InitContainers   []corev1.Container            `json:"initContainers"`
	Containers       []corev1.Container            `json:"containers"`
	Volumes          []corev1.Volume               `json:"volumes"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets"`
}

func (p *podDataInfo) addInjectStatusAnnotation(sic SidecarInjectionSpec) string {
	status := &SidecarInjectionStatus{Version: p.injectTemplateVersion}
	for _, c := range sic.InitContainers {
		status.InitContainers = append(status.InitContainers, corev1.Container{
			Name: c.Name,
		})
	}
	for _, c := range sic.Containers {
		status.Containers = append(status.Containers, corev1.Container{
			Name: c.Name,
		})
	}
	for _, c := range sic.Volumes {
		status.Volumes = append(status.Volumes, corev1.Volume{
			Name: c.Name,
		})
	}
	for _, c := range sic.ImagePullSecrets {
		status.ImagePullSecrets = append(status.ImagePullSecrets, corev1.LocalObjectReference{
			Name: c.Name,
		})
	}
	statusAnnotationValue, _ := json.Marshal(status)
	result := string(statusAnnotationValue)
	if len(result) != 0 {
		p.injectedAnnotations[annotation.SidecarStatus.Name] = result
	}
	return result
}
