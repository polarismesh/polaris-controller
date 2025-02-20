// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package inject

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/fsnotify/fsnotify"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apismetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/common"
	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

var (
	runtimeScheme = runtime.NewScheme()
	codecs        = serializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecs.UniversalDeserializer()
)

func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = v1beta1.AddToScheme(runtimeScheme)
	_ = v1.AddToScheme(runtimeScheme)
}

const (
	watchDebounceDelay = 100 * time.Millisecond
)

// Webhook implements a mutating webhook for automatic proxy injection.
type Webhook struct {
	defaultSidecarMode utils.SidecarMode
	templateConfig     *config.SafeTemplateConfig
	templateFileConfig config.TemplateFileConfig

	healthCheckInterval time.Duration
	healthCheckFile     string

	server    *http.Server
	watcher   *fsnotify.Watcher
	k8sClient kubernetes.Interface
}

// WebhookParameters configures parameters for the sidecar injection
// webhook.
type WebhookParameters struct {
	// DefaultSidecarMode polaris-sidecar 默认的运行模式
	DefaultSidecarMode utils.SidecarMode

	// TemplateFileConfig 模板配置文件位置
	TemplateFileConfig config.TemplateFileConfig

	// Port is the webhook port, e.g. typically 443 for https.
	Port int

	// HealthCheckInterval configures how frequently the health check
	// file is updated. Value of zero disables the health check
	// update.
	HealthCheckInterval time.Duration

	// HealthCheckFile specifies the path to the health check file
	// that is periodically updated.
	HealthCheckFile string

	// Use an existing mux instead of creating our own.
	Mux *http.ServeMux

	// 操作 k8s 资源的客户端
	Client kubernetes.Interface
}

// NewWebhook creates a new instance of a mutating webhook for automatic sidecar injection.
func NewWebhook(p WebhookParameters) (*Webhook, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// watch the parent directory of the target files so we can catch
	// symlink updates of k8s ConfigMaps volumes.
	for _, file := range p.TemplateFileConfig.GetWatchList() {
		watchDir, _ := filepath.Split(file)
		if err := watcher.Add(watchDir); err != nil {
			return nil, fmt.Errorf("could not watch %v: %v", file, err)
		}
	}
	templateConfig, err := config.NewTemplateConfig(p.TemplateFileConfig)
	if err != nil {
		log.InjectScope().Errorf("NewTemplateConfig failed: %v", err)
		return nil, err
	}
	wh := &Webhook{
		templateConfig:      templateConfig,
		templateFileConfig:  p.TemplateFileConfig,
		watcher:             watcher,
		healthCheckInterval: p.HealthCheckInterval,
		healthCheckFile:     p.HealthCheckFile,
		k8sClient:           p.Client,
		defaultSidecarMode:  p.DefaultSidecarMode,
	}

	if p.Mux != nil {
		p.Mux.HandleFunc("/inject", wh.serveInject)
	} else {
		wh.server = &http.Server{
			Addr: fmt.Sprintf(":%v", p.Port),
			// mtls disabled because apiserver webhook cert usage is still TBD.
			TLSConfig: &tls.Config{GetCertificate: wh.templateConfig.GetCert},
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/inject", wh.serveInject)
		wh.server.Handler = mux
	}
	return wh, nil
}

// Run implements the webhook server
func (wh *Webhook) Run(stop <-chan struct{}) {
	if wh.server != nil {
		go func() {
			if err := wh.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.InjectScope().Fatalf("admission webhook ListenAndServeTLS failed: %v", err)
			}
		}()
		defer wh.server.Close()
	}
	defer wh.watcher.Close()

	var healthC <-chan time.Time
	if wh.healthCheckInterval != 0 && wh.healthCheckFile != "" {
		t := time.NewTicker(wh.healthCheckInterval)
		healthC = t.C
		defer t.Stop()
	}
	var timerC <-chan time.Time

	for {
		select {
		case <-timerC:
			timerC = nil
			if err := wh.templateConfig.UpdateTemplateConfig(wh.templateFileConfig); err != nil {
				log.InjectScope().Errorf("UpdateTemplateConfig failed: %v", err)
			}
		case event := <-wh.watcher.Events:
			log.InjectScope().Infof("Injector watch update: %+v", event)
			// use a timer to debounce configuration updates
			if (event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Create != 0) && timerC == nil {
				timerC = time.After(watchDebounceDelay)
			}
		case err := <-wh.watcher.Errors:
			log.InjectScope().Errorf("Watcher error: %v", err)
		case <-healthC:
			content := []byte(`ok`)
			if err := os.WriteFile(wh.healthCheckFile, content, 0o644); err != nil {
				log.InjectScope().Errorf("Health check update of %q failed: %v", wh.healthCheckFile, err)
			}
		case <-stop:
			return
		}
	}
}

// It would be great to use https://github.com/mattbaird/jsonpatch to
// generate RFC6902 JSON patches. Unfortunately, it doesn't produce
// correct patches for object removal. Fortunately, our patching needs
// are fairly simple so generating them manually isn't horrible (yet).
type Rfc6902PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

type PolarisGoConfig struct {
	Name          string
	Namespace     string
	PolarisServer string
}

func EnableMtls(pod *corev1.Pod) bool {
	return enableMtls(pod)
}

func enableMtls(pod *corev1.Pod) bool {
	value, ok := pod.Annotations[utils.PolarisTLSMode]
	if ok && value != utils.MTLSModeNone {
		return true
	}
	return false
}

// nolint
// addPolarisConfigToInitContainerEnv 将polaris-sidecar 的配置注入到init container中
func (wh *Webhook) addPolarisConfigToInitContainerEnv(add *corev1.Container) error {
	cfgTpl, err := wh.k8sClient.CoreV1().ConfigMaps(common.PolarisControllerNamespace).
		Get(context.TODO(), utils.PolarisGoConfigFileTpl, metav1.GetOptions{})
	if err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}

	tmp, err := (&template.Template{}).Parse(cfgTpl.Data["polaris.yaml"])
	if err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}
	buf := new(bytes.Buffer)
	if err := tmp.Execute(buf, PolarisGoConfig{
		Name:          utils.PolarisGoConfigFile,
		PolarisServer: common.PolarisServerGrpcAddress,
	}); err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}

	// 获取 polaris-sidecar 配置
	configMap := corev1.ConfigMap{}
	str := buf.String()
	if err := yaml.NewYAMLOrJSONDecoder(strings.NewReader(str), len(str)).Decode(&configMap); err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}

	add.Env = append(add.Env, corev1.EnvVar{
		Name:  utils.PolarisGoConfigFile,
		Value: configMap.Data["polaris.yaml"],
	})
	return nil
}

// nolint
// currently we assume that polaris-security deploy into polaris-system namespace.
const rootNamespace = "polaris-system"

// nolint
// ensureRootCertExist ensure that we have rootca pem secret in current namespace
func (wh *Webhook) ensureRootCertExist(pod *corev1.Pod) error {
	if !enableMtls(pod) {
		return nil
	}
	ns := pod.Namespace
	_, err := wh.k8sClient.CoreV1().Secrets(ns).Get(context.TODO(), utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}
	secret, err := wh.k8sClient.CoreV1().Secrets(rootNamespace).Get(context.TODO(), utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// copy all data from root namespace rootca secret.
	s := &corev1.Secret{}
	s.Data = secret.Data
	s.StringData = secret.StringData
	s.Name = utils.PolarisSidecarRootCert
	_, err = wh.k8sClient.CoreV1().Secrets(ns).Create(context.TODO(), s, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) || errors.IsConflict(err) {
		return nil
	}
	return err
}

func addSecurityContext(target *corev1.PodSecurityContext, basePath string) (patch []Rfc6902PatchOperation) {
	patch = append(patch, Rfc6902PatchOperation{
		Op:    "add",
		Path:  basePath,
		Value: target,
	})
	return patch
}

func addPodDNSConfig(target *corev1.PodDNSConfig, basePath string) (patch []Rfc6902PatchOperation) {
	patch = append(patch, Rfc6902PatchOperation{
		Op:    "add",
		Path:  basePath,
		Value: target,
	})
	return patch
}

// escape JSON Pointer value per https://tools.ietf.org/html/rfc6901
func escapeJSONPointerValue(in string) string {
	step := strings.Replace(in, "~", "~0", -1)
	return strings.Replace(step, "/", "~1", -1)
}

// nolint
// adds labels to the target spec, will not overwrite label's value if it already exists
func addLabels(target map[string]string, added map[string]string) []Rfc6902PatchOperation {
	patches := []Rfc6902PatchOperation{}

	addedKeys := make([]string, 0, len(added))
	for key := range added {
		addedKeys = append(addedKeys, key)
	}
	sort.Strings(addedKeys)

	for _, key := range addedKeys {
		value := added[key]
		if len(value) == 0 {
			continue
		}

		patch := Rfc6902PatchOperation{
			Op:    "add",
			Path:  "/metadata/labels/" + escapeJSONPointerValue(key),
			Value: value,
		}

		if target == nil {
			target = map[string]string{}
			patch.Path = "/metadata/labels"
			patch.Value = map[string]string{
				key: value,
			}
		}

		if target[key] == "" {
			patches = append(patches, patch)
		}
	}

	return patches
}

func updateAnnotation(target map[string]string, added map[string]string) (patch []Rfc6902PatchOperation) {
	// To ensure deterministic patches, we sort the keys
	var keys []string
	for k := range added {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := added[key]
		if len(value) == 0 {
			continue
		}
		if target == nil {
			target = map[string]string{}
			patch = append(patch, Rfc6902PatchOperation{
				Op:   "add",
				Path: "/metadata/annotations",
				Value: map[string]string{
					key: value,
				},
			})
		} else {
			op := "add"
			if target[key] != "" {
				op = "replace"
			}
			patch = append(patch, Rfc6902PatchOperation{
				Op:    op,
				Path:  "/metadata/annotations/" + escapeJSONPointerValue(key),
				Value: value,
			})
		}
	}
	return patch
}

func createPatch(opt *PatchOptions) ([]byte, error) {
	sidecarMode := opt.SidecarMode
	pod := opt.Pod
	prevStatus := opt.PrevStatus
	sic := opt.Sic
	annotations := opt.Annotations
	var patch []Rfc6902PatchOperation

	patchBuilder, ok := _PatchBuilders[utils.ParseSidecarModeName(sidecarMode)]
	if !ok {
		return nil, errors.NewInternalError(fmt.Errorf("sidecar-mode %+v not found target patch builder", sidecarMode))
	}

	// Remove any containers previously injected by kube-inject using
	// container and volume name as unique key for removal.
	removeInitContainerPatch, err := patchBuilder.PatchContainer(&OperateContainerRequest{
		Type:     PatchType_Remove,
		BasePath: "/spec/initContainers",
		Source:   pod.Spec.InitContainers,
		External: prevStatus.InitContainers,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	removeContainerPatch, err := patchBuilder.PatchContainer(&OperateContainerRequest{
		Type:     PatchType_Remove,
		BasePath: "/spec/containers",
		Source:   pod.Spec.Containers,
		External: prevStatus.Containers,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	removeVolumesPatch, err := patchBuilder.PatchVolumes(&OperateVolumesRequest{
		Type:     PatchType_Remove,
		BasePath: "/spec/volumes",
		Source:   pod.Spec.Volumes,
		External: prevStatus.Volumes,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	removeImagePullSecretsPatch, err := patchBuilder.PatchImagePullSecrets(&OperateImagePullSecretsRequest{
		Type:     PatchType_Remove,
		BasePath: "/spec/imagePullSecrets",
		Source:   pod.Spec.ImagePullSecrets,
		External: prevStatus.ImagePullSecrets,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}

	//
	addInitContainerPatch, err := patchBuilder.PatchContainer(&OperateContainerRequest{
		Type:     PatchType_Add,
		BasePath: "/spec/initContainers",
		Source:   pod.Spec.InitContainers,
		External: sic.InitContainers,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	addContainerPatch, err := patchBuilder.PatchContainer(&OperateContainerRequest{
		Type:     PatchType_Add,
		BasePath: "/spec/containers",
		Source:   pod.Spec.Containers,
		External: sic.Containers,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	updateContainerPatch, err := patchBuilder.PatchContainer(&OperateContainerRequest{
		Type:     PatchType_Update,
		BasePath: "/spec/containers",
		Source:   pod.Spec.Containers,
		External: sic.Containers,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}

	addVolumePatch, err := patchBuilder.PatchVolumes(&OperateVolumesRequest{
		Type:     PatchType_Add,
		BasePath: "/spec/volumes",
		Source:   pod.Spec.Volumes,
		External: sic.Volumes,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}
	addImagePullSecretsPatch, err := patchBuilder.PatchImagePullSecrets(&OperateImagePullSecretsRequest{
		Type:     PatchType_Add,
		BasePath: "/spec/imagePullSecrets",
		Source:   pod.Spec.ImagePullSecrets,
		External: sic.ImagePullSecrets,
		Option:   opt,
	})
	if err != nil {
		return nil, err
	}

	//
	patch = append(patch, removeInitContainerPatch...)
	patch = append(patch, removeContainerPatch...)
	patch = append(patch, removeVolumesPatch...)
	patch = append(patch, removeImagePullSecretsPatch...)

	//
	patch = append(patch, addInitContainerPatch...)
	patch = append(patch, addContainerPatch...)
	patch = append(patch, updateContainerPatch...)
	patch = append(patch, addVolumePatch...)
	patch = append(patch, addImagePullSecretsPatch...)

	if sic.DNSConfig != nil {
		patch = append(patch, addPodDNSConfig(sic.DNSConfig, "/spec/dnsConfig")...)
	}

	if pod.Spec.SecurityContext != nil {
		patch = append(patch, addSecurityContext(pod.Spec.SecurityContext, "/spec/securityContext")...)
	}

	patch = append(patch, updateAnnotation(pod.Annotations, annotations)...)

	return json.Marshal(patch)
}

// Retain deprecated hardcoded container and volumes names to aid in
// backwards compatible migration to the new SidecarInjectionStatus.
var (
	legacyInitContainerNames = []corev1.Container{corev1.Container{Name: "istio-init"}, corev1.Container{Name: "enable-core-dump"}}
	legacyContainerNames     = []corev1.Container{corev1.Container{Name: ProxyContainerName}}
	legacyVolumeNames        = []corev1.Volume{corev1.Volume{Name: "polaris-certs"}, corev1.Volume{Name: "polaris-envoy"}}
)

func injectionStatus(pod *corev1.Pod) *SidecarInjectionStatus {
	var statusBytes []byte
	if pod.ObjectMeta.Annotations != nil {
		if value, ok := pod.ObjectMeta.Annotations[annotation.SidecarStatus.Name]; ok {
			statusBytes = []byte(value)
		}
	}

	// default case when injected pod has explicit status
	var iStatus SidecarInjectionStatus
	if err := json.Unmarshal(statusBytes, &iStatus); err == nil {
		// heuristic assumes status is valid if any o
		// lists is non-empty.
		if len(iStatus.InitContainers) != 0 ||
			len(iStatus.Containers) != 0 ||
			len(iStatus.Volumes) != 0 ||
			len(iStatus.ImagePullSecrets) != 0 {
			return &iStatus
		}
	}

	// backwards compatibility case when injected pod has legacy
	// status. Infer status from the list of legacy hardcoded
	// container and volume names.
	return &SidecarInjectionStatus{
		InitContainers: legacyInitContainerNames,
		Containers:     legacyContainerNames,
		Volumes:        legacyVolumeNames,
	}
}

func toV1AdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{Result: &metav1.Status{Message: err.Error()}}
}

func toV1beta1AdmissionResponse(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{Result: &apismetav1.Status{Message: err.Error()}}
}

func (wh *Webhook) injectV1beta1(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	req := ar.Request
	log.InjectScope().Infof("Object: %v", string(req.Object.Raw))
	log.InjectScope().Infof("OldObject: %v", string(req.OldObject.Raw))
	// 解析原始对象
	podInfo, err := assignPodDataInfo(req.Object.Raw, req.Namespace, wh)
	if err != nil {
		handleError(fmt.Sprintf("Could not unmarshal raw object: %v %s", err,
			string(req.Object.Raw)))
		return toV1beta1AdmissionResponse(err)
	}
	log.InjectScope().Infof("AdmissionReview for Kind=%v Namespace=%v Name=%v (%v) UID=%v Rfc6902PatchOperation=%v "+
		"UserInfo=%v", req.Kind, req.Namespace, req.Name, podInfo.podName, req.UID, req.Operation, req.UserInfo)
	// 获取准入注入patch
	patchBytes, err := wh.getPodPatch(podInfo)
	if err != nil {
		handleError(fmt.Sprintf("Could not get admission patch: %v", err))
		return toV1beta1AdmissionResponse(err)
	}
	// 跳过注入
	if patchBytes == nil {
		log.InjectScope().Infof("[Webhook] skipping %s/%s due to empty patchBytes", req.Namespace, podInfo.podName)
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}
	log.InjectScope().Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
	reviewResponse := v1beta1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1beta1.PatchType {
			pt := v1beta1.PatchTypeJSONPatch
			return &pt
		}(),
	}
	return &reviewResponse
}

// inject istio 核心准入注入逻辑
func (wh *Webhook) injectV1(ar *v1.AdmissionReview) *v1.AdmissionResponse {
	req := ar.Request
	log.InjectScope().Infof("Object: %v", string(req.Object.Raw))
	log.InjectScope().Infof("OldObject: %v", string(req.OldObject.Raw))
	// 解析原始对象
	podInfo, err := assignPodDataInfo(req.Object.Raw, req.Namespace, wh)
	if err != nil {
		handleError(fmt.Sprintf("Could not unmarshal raw object: %v %s", err,
			string(req.Object.Raw)))
		return toV1AdmissionResponse(err)
	}
	log.InjectScope().Infof("AdmissionReview for Kind=%v Namespace=%v Name=%v (%v) UID=%v Rfc6902PatchOperation=%v "+
		"UserInfo=%v", req.Kind, req.Namespace, req.Name, podInfo.podName, req.UID, req.Operation, req.UserInfo)
	// 获取准入注入patch
	patchBytes, err := wh.getPodPatch(podInfo)
	if err != nil {
		handleError(fmt.Sprintf("Could not get admission patch: %v", err))
		return toV1AdmissionResponse(err)
	}
	// 跳过注入
	if patchBytes == nil {
		log.InjectScope().Infof("[Webhook] skipping %s/%s due to empty patchBytes", req.Namespace, podInfo.podName)
		return &v1.AdmissionResponse{
			Allowed: true,
		}
	}
	log.InjectScope().Infof("AdmissionResponse: patch=%v\n", string(patchBytes))
	reviewResponse := v1.AdmissionResponse{
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *v1.PatchType {
			pt := v1.PatchTypeJSONPatch
			return &pt
		}(),
	}
	return &reviewResponse
}

func (wh *Webhook) serveInject(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	log.InjectScope().Infof("[Webhook] receive webhook request path %s, data %s", r.URL.RawPath, string(body))
	if len(body) == 0 {
		handleError("no body found")
		http.Error(w, "no body found", http.StatusBadRequest)
		return
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		handleError(fmt.Sprintf("contentType=%s, expect application/json", contentType))
		http.Error(w, "invalid Content-Type, want `application/json`", http.StatusUnsupportedMediaType)
		return
	}

	// gen response based on type of request
	obj, gvk, err := deserializer.Decode(body, nil, nil)
	if err != nil {
		msg := fmt.Sprintf("Could not decode body: %v", err)
		handleError(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	var responseObj runtime.Object
	switch *gvk {
	case v1beta1.SchemeGroupVersion.WithKind("AdmissionReview"):
		requestAdmissionReview, ok := obj.(*v1beta1.AdmissionReview)
		if !ok {
			log.InjectScope().Errorf("[Webhook] expected v1beta1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &v1beta1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = wh.injectV1beta1(requestAdmissionReview)
		responseAdmissionReview.Response.UID = requestAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	case v1.SchemeGroupVersion.WithKind("AdmissionReview"):
		requestAdmissionReview, ok := obj.(*v1.AdmissionReview)
		if !ok {
			log.InjectScope().Errorf("[Webhook] expected v1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &v1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = wh.injectV1(requestAdmissionReview)
		responseAdmissionReview.Response.UID = requestAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	default:
		msg := fmt.Sprintf("[Webhook] unsupported group version kind: %v", gvk)
		log.InjectScope().Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
	}

	resp, err := json.Marshal(responseObj)
	if err != nil {
		log.InjectScope().Errorf("Could not encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	if _, err := w.Write(resp); err != nil {
		log.InjectScope().Errorf("Could not write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}

func handleError(message string) {
	log.InjectScope().Errorf(message)
}
