package inject

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

func TestGetInjectModePriorityAndUnknown(t *testing.T) {
	tests := []struct {
		name        string
		defaultMode utils.SidecarMode
		namespace   string
		nsLabels    map[string]string
		annotations map[string]string
		want        utils.SidecarMode
	}{
		{
			name:        "java agent annotation wins",
			defaultMode: utils.SidecarForMesh,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "dns"},
			annotations: map[string]string{utils.AnnotationKeyInjectJavaAgent: "true"},
			want:        utils.SidecarForJavaAgent,
		},
		{
			name:        "mesh on-demand annotation wins",
			defaultMode: utils.SidecarForUnknown,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "dns"},
			annotations: map[string]string{utils.SidecarEnvoyInjectKey: "true"},
			want:        utils.SidecarForMesh,
		},
		{
			name:        "pod sidecar mode wins over namespace",
			defaultMode: utils.SidecarForUnknown,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "mesh"},
			annotations: map[string]string{utils.PolarisSidecarMode: "dns"},
			want:        utils.SidecarForDns,
		},
		{
			name:        "empty pod annotation falls back to namespace",
			defaultMode: utils.SidecarForUnknown,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "dns"},
			annotations: map[string]string{utils.PolarisSidecarMode: ""},
			want:        utils.SidecarForDns,
		},
		{
			name:        "invalid pod annotation does not inject",
			defaultMode: utils.SidecarForDns,
			namespace:   "default",
			annotations: map[string]string{utils.PolarisSidecarMode: "dnsagent"},
			want:        utils.SidecarForUnknown,
		},
		{
			name:        "namespace label wins over default",
			defaultMode: utils.SidecarForUnknown,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "mesh"},
			want:        utils.SidecarForMesh,
		},
		{
			name:        "empty namespace label falls back to default",
			defaultMode: utils.SidecarForDns,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: ""},
			want:        utils.SidecarForDns,
		},
		{
			name:        "invalid namespace label does not inject",
			defaultMode: utils.SidecarForDns,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "dnsagent"},
			want:        utils.SidecarForUnknown,
		},
		{
			name:        "none default does not inject",
			defaultMode: utils.ParseSidecarMode("none"),
			namespace:   "default",
			want:        utils.SidecarForUnknown,
		},
		{
			name:        "none pod annotation does not inject",
			defaultMode: utils.SidecarForDns,
			namespace:   "default",
			annotations: map[string]string{utils.PolarisSidecarMode: "none"},
			want:        utils.SidecarForUnknown,
		},
		{
			name:        "none namespace label does not inject",
			defaultMode: utils.SidecarForDns,
			namespace:   "default",
			nsLabels:    map[string]string{utils.PolarisSidecarModeLabel: "none"},
			want:        utils.SidecarForUnknown,
		},
		{
			name:        "no mode does not inject",
			defaultMode: utils.SidecarForUnknown,
			namespace:   "default",
			want:        utils.SidecarForUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   tc.namespace,
					Labels: tc.nsLabels,
				},
			}
			wh := &Webhook{
				k8sClient:          fake.NewSimpleClientset(namespace),
				defaultSidecarMode: tc.defaultMode,
				templateConfig:     &config.SafeTemplateConfig{},
			}
			p := &podDataInfo{
				podObject: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-pod",
						Namespace:   tc.namespace,
						Annotations: tc.annotations,
					},
				},
				podName: "test-pod",
			}

			if got := getInjectMode(wh, p); got != tc.want {
				t.Fatalf("getInjectMode() = %v, want %v", got, tc.want)
			}
		})
	}
}
