package inject

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
)

func TestMeshInjectRequired(t *testing.T) {
	// 通用测试配置
	defaultTemplate := &config.TemplateConfig{
		NeverInjectSelector:  []metav1.LabelSelector{{MatchLabels: map[string]string{"security": "high"}}},
		AlwaysInjectSelector: []metav1.LabelSelector{{MatchLabels: map[string]string{"env": "prod"}}},
		Policy:               config.InjectionPolicyEnabled,
	}

	testCases := []struct {
		name     string
		podSetup func(*corev1.Pod)
		expected bool
	}{
		{
			name: "当启用HostNetwork时无需注入",
			podSetup: func(p *corev1.Pod) {
				p.Spec.HostNetwork = true
			},
			expected: false,
		},
		{
			name: "在kube-system命名空间中跳过注入",
			podSetup: func(p *corev1.Pod) {
				p.Namespace = "kube-system"
			},
			expected: false,
		},
		{
			name: "当注解明确启用时应该注入",
			podSetup: func(p *corev1.Pod) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
			},
			expected: true,
		},
		{
			name: "当注解明确禁用时不注入",
			podSetup: func(p *corev1.Pod) {
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "false"}
			},
			expected: false,
		},
		{
			name: "当匹配NeverInjectSelector时不注入",
			podSetup: func(p *corev1.Pod) {
				p.Labels = map[string]string{"security": "high"}
			},
			expected: false,
		},
		{
			name: "当匹配AlwaysInjectSelector时强制注入",
			podSetup: func(p *corev1.Pod) {
				p.Labels = map[string]string{"env": "prod"}
			},
			expected: true,
		},
		{
			name: "默认策略启用时自动注入",
			podSetup: func(p *corev1.Pod) {
				// 无额外配置
			},
			expected: true,
		},
		{
			name: "当策略禁用时需要显式启用才注入",
			podSetup: func(p *corev1.Pod) {
				defaultTemplate.Policy = config.InjectionPolicyDisabled
				p.Annotations = map[string]string{annotation.SidecarInject.Name: "true"}
			},
			expected: true,
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
				tc.podSetup(pod)
			}

			podInfo := &podDataInfo{
				podObject:            pod,
				injectTemplateConfig: defaultTemplate,
				podName:              pod.Name,
			}

			result := wh.meshInjectRequired(podInfo)
			if result != tc.expected {
				t.Errorf("%s: 期望 %v 但得到 %v", tc.name, tc.expected, result)
			}
		})
	}
}
