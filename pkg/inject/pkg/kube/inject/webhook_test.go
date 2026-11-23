package inject

import (
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

func TestInjectV1UnknownModeAllowsPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
	}
	raw, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("marshal pod: %v", err)
	}
	ar := &admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Object:    runtime.RawExtension{Raw: raw},
			Namespace: "default",
			Operation: admissionv1.Create,
		},
	}
	wh := &Webhook{
		k8sClient:          fake.NewSimpleClientset(),
		defaultSidecarMode: utils.SidecarForUnknown,
	}

	response := wh.injectV1(ar)
	if !response.Allowed {
		t.Fatalf("response.Allowed = false, want true")
	}
	if response.Patch != nil {
		t.Fatalf("response.Patch = %s, want nil", response.Patch)
	}

	podInfo := &podDataInfo{
		podObject:  pod,
		podName:    "test-pod",
		injectMode: utils.SidecarForUnknown,
	}
	if patch, err := wh.getPodPatch(podInfo); err != nil || patch != nil {
		t.Fatalf("getPodPatch() = (%s, %v), want (nil, nil)", patch, err)
	}
}
