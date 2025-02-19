package inject

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

func TestCommonInjectRequired(t *testing.T) {
	// 通用测试配置
	defaultTemplate := &config.TemplateConfig{
		NeverInjectSelector: []metav1.LabelSelector{{MatchLabels: map[string]string{
			utils.PolarisInjectionKey: utils.PolarisInjectionDisabled}}},
		AlwaysInjectSelector: []metav1.LabelSelector{{MatchLabels: map[string]string{
			utils.PolarisInjectionKey: utils.PolarisInjectionEnabled}}},
		Policy: config.InjectionPolicyEnabled,
	}

	// 参考istio官方定义
	// https://istio.io/latest/zh/docs/ops/common-problems/injection/
	testCases := []struct {
		name     string
		podSetup func(*corev1.Pod, *config.TemplateConfig)
		expected bool
	}{
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
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionDisabled}
			},
			expected: false,
		},
		{
			name: "Label白名单功能,当匹配AlwaysInjectSelector时注入",
			podSetup: func(p *corev1.Pod, _ *config.TemplateConfig) {
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionEnabled}
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
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionDisabled}
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
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionEnabled}
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
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionEnabled}
				defaultTemplate.Policy = config.InjectionPolicyDisabled
			},
			expected: true,
		},
		{
			name: "Label黑名单,Policy白名单,不注入",
			podSetup: func(p *corev1.Pod, defaultTemplate *config.TemplateConfig) {
				p.Labels = map[string]string{utils.PolarisInjectionKey: utils.PolarisInjectionDisabled}
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
				injectTemplateConfig: defaultTemplate,
				podName:              pod.Name,
			}

			result := wh.commonInjectRequired(podInfo)
			if result != tc.expected {
				t.Errorf("%s: 期望 %v 但得到 %v", tc.name, tc.expected, result)
			}
		})
	}
}
