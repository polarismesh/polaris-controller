package inject

import (
	"encoding/json"
	"log"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

func TestRequireInject(t *testing.T) {
	// 通用测试配置
	defaultTemplate := &config.TemplateConfig{
		NeverInjectSelector: []metav1.LabelSelector{{MatchLabels: map[string]string{
			utils.InjectAdmissionKey: utils.InjectAdmissionValueDisabled}}},
		AlwaysInjectSelector: []metav1.LabelSelector{{MatchLabels: map[string]string{
			utils.InjectAdmissionKey: utils.InjectAdmissionValueEnabled}}},
		Policy: config.InjectionPolicyEnabled,
	}
	str, _ := json.Marshal(defaultTemplate.NeverInjectSelector)
	log.Printf("defaultTemplate.NeverInjectSelector: %s", str)

	// 参考istio官方定义
	// https://istio.io/latest/zh/docs/ops/common-problems/injection/
	testCases := []struct {
		name       string
		podSetup   func(*corev1.Pod, *config.TemplateConfig)
		injectMode utils.SidecarMode
		expected   bool
	}{
		{
			name: "mesh类型,当启用HostNetwork时无需注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Spec.HostNetwork = true
			},
			injectMode: utils.SidecarForMesh,
			expected:   false,
		},
		{
			name: "dns类型,当启用HostNetwork时无需注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Spec.HostNetwork = true
			},
			injectMode: utils.SidecarForDns,
			expected:   false,
		},
		{
			name: "javaagent,当启用HostNetwork时不影响注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Spec.HostNetwork = true
			},
			injectMode: utils.SidecarForJavaAgent,
			expected:   true,
		},
		{
			name: "在kube-system命名空间中跳过注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Namespace = "kube-system"
			},
			expected: false,
		},
		{
			name: "Annotation白名单,当注解明确启用时应该注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
			},
			expected: true,
		},
		{
			name: "Annotation黑名单,当注解明确禁用时不注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "false"}
			},
			expected: false,
		},
		{
			name: "Label黑名单,当匹配NeverInjectSelector时不注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueDisabled}
			},
			expected: false,
		},
		{
			name: "Label白名单功能,当匹配AlwaysInjectSelector时注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueEnabled}
			},
			expected: true,
		},
		{
			name: "Policy白名单,默认策略启用时自动注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				defaultTemplate.Policy = config.InjectionPolicyEnabled
			},
			expected: true,
		},
		{
			name: "Policy黑名单,默认策略禁用时不注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				defaultTemplate.Policy = config.InjectionPolicyDisabled
			},
			expected: false,
		},
		{
			name: "Annotation白名单,Label黑名单,注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueDisabled}
			},
			expected: true,
		},
		{
			name: "Annotation白名单,Policy黑名单,注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
				defaultTemplate.Policy = config.InjectionPolicyDisabled
			},
			expected: true,
		},
		{
			name: "Annotation黑名单,Label白名单,不注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "false"}
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueEnabled}
			},
			expected: false,
		},
		{
			name: "Annotation黑名单,Policy白名单,不注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "false"}
				defaultTemplate.Policy = config.InjectionPolicyEnabled
			},
			expected: false,
		},
		{
			name: "Label白名单功能,Policy黑名单,注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueEnabled}
				defaultTemplate.Policy = config.InjectionPolicyDisabled
			},
			expected: true,
		},
		{
			name: "Label黑名单,Policy白名单,不注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				p.Labels = map[string]string{utils.InjectAdmissionKey: utils.InjectAdmissionValueDisabled}
				defaultTemplate.Policy = config.InjectionPolicyEnabled
			},
			expected: false,
		},
		{
			name: "Policy为空,默认策略为空时,完全禁止注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				defaultTemplate.Policy = ""
			},
			expected: false,
		},
		{
			name: "Policy为空,尽管Annotation白名单,仍会禁止注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
				defaultTemplate.Policy = ""
			},
			expected: false,
		},
	}

	wh := &Webhook{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pod",
					Namespace:   "default",
					Annotations: make(map[string]string),
					Labels:      make(map[string]string),
				},
				Spec: corev1.PodSpec{},
			}
			if tc.podSetup != nil {
				tc.podSetup(pod, defaultTemplate)
			}

			podInfo := &podDataInfo{
				podObject:            pod,
				injectMode:           tc.injectMode,
				injectTemplateConfig: defaultTemplate,
				podName:              pod.Name,
			}

			result := wh.requireInject(podInfo)
			if result != tc.expected {
				t.Errorf("%s: 期望 %v 但得到 %v", tc.name, tc.expected, result)
			}
		})
	}
}
