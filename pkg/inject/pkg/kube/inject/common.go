package inject

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

// getInjectMode determines the injection mode for the pod based on annotations and namespace labels.
// The priority order is:
// 1. Pod annotations for Java agent mode
// 2. Pod annotations for Mesh mode
// 3. Pod annotations for custom sidecar mode
// 4. Namespace labels
// 5. Default sidecar mode from webhook configuration
func getInjectMode(wh *Webhook, p *podDataInfo) utils.SidecarMode {
	pod := p.podObject
	namespace := pod.Namespace

	// Check injection mode by priority
	switch {
	case isJavaAgentMode(pod.Annotations):
		log.InjectScope().Infof("inject pod namespace %q mode is java agent", namespace)
		return utils.SidecarForJavaAgent
	case isMeshMode(pod.Annotations):
		log.InjectScope().Infof("inject pod namespace %q mode is mesh envoy", namespace)
		setMeshEnvoyConfig(wh, pod.Annotations)
		return utils.SidecarForMesh
	case hasSidecarModeAnnotation(pod.Annotations):
		return utils.ParseSidecarMode(pod.Annotations[utils.PolarisSidecarMode])
	}

	return getNamespaceInjectMode(wh, namespace)
}

// isJavaAgentMode checks if the pod should use Java agent injection mode
func isJavaAgentMode(annotations map[string]string) bool {
	val, ok := annotations[utils.AnnotationKeyInjectJavaAgent]
	return ok && val == utils.InjectionValueTrue
}

// isMeshMode checks if the pod should use Mesh injection mode
func isMeshMode(annotations map[string]string) bool {
	val, ok := annotations[utils.SidecarEnvoyInjectKey]
	return ok && val == utils.InjectionValueTrue
}

// hasSidecarModeAnnotation checks if the pod has custom sidecar mode annotation
func hasSidecarModeAnnotation(annotations map[string]string) bool {
	_, ok := annotations[utils.PolarisSidecarMode]
	return ok
}

// setMeshEnvoyConfig sets the Mesh Envoy configuration metadata
func setMeshEnvoyConfig(wh *Webhook, annotations map[string]string) {
	wh.templateConfig.SetMeshEnvoyConfigWithKV(utils.SidecarEnvoyInjectProxyKey,
		annotations[utils.SidecarEnvoyInjectKey])
}

// getNamespaceInjectMode gets injection mode from namespace labels or returns default mode
func getNamespaceInjectMode(wh *Webhook, namespace string) utils.SidecarMode {
	ns, err := wh.k8sClient.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		log.InjectScope().Errorf("get pod namespace %q failed: %v", namespace, err)
		return wh.defaultSidecarMode
	}

	if val, ok := ns.Labels[utils.PolarisSidecarModeLabel]; ok {
		return utils.ParseSidecarMode(val)
	}
	return wh.defaultSidecarMode
}

const (
	workloadDeployment = "Deployment"
	workloadReplicaSet = "ReplicaSet"

	podTemplateHashLabel = "pod-template-hash"
)

// getOwnerMetaAndType 获取Pod创建对象的元数据和类型信息
func getOwnerMetaAndType(pod *corev1.Pod) (*metav1.ObjectMeta, *metav1.TypeMeta) {
	deployMeta := pod.ObjectMeta.DeepCopy()
	typeMetadata := &metav1.TypeMeta{
		Kind:       "Pod",
		APIVersion: "v1",
	}

	// 如果不是通过Controller创建的Pod，直接返回Pod的元数据
	if len(pod.GenerateName) == 0 {
		return deployMeta, typeMetadata
	}

	// 获取Pod的Controller引用
	controllerRef := findControllerRef(pod.GetOwnerReferences())
	if controllerRef.Name == "" {
		return deployMeta, typeMetadata
	}

	// 更新类型元数据为Controller的信息
	typeMetadata.APIVersion = controllerRef.APIVersion
	typeMetadata.Kind = controllerRef.Kind

	// 处理Deployment特殊场景
	if isDeploymentReplicaSet(typeMetadata.Kind, controllerRef.Name, pod.Labels) {
		deployMeta.Name = getDeploymentName(controllerRef.Name, pod.Labels[podTemplateHashLabel])
		typeMetadata.Kind = workloadDeployment
	} else {
		deployMeta.Name = controllerRef.Name
	}

	return deployMeta, typeMetadata
}

// findControllerRef 从OwnerReferences中查找Pod的Controller引用
func findControllerRef(refs []metav1.OwnerReference) metav1.OwnerReference {
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller {
			return ref
		}
	}
	return metav1.OwnerReference{}
}

// isDeploymentReplicaSet 判断是否为Deployment创建的ReplicaSet
func isDeploymentReplicaSet(kind, name string, labels map[string]string) bool {
	return kind == workloadReplicaSet && labels[podTemplateHashLabel] != "" &&
		strings.HasSuffix(name, labels[podTemplateHashLabel])
}

// getDeploymentName 从ReplicaSet名称中提取Deployment名称
func getDeploymentName(replicaSetName, podTemplateHash string) string {
	return strings.TrimSuffix(replicaSetName, "-"+podTemplateHash)
}
