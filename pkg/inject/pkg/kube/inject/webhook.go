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
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config/mesh"
	"k8s.io/apimachinery/pkg/api/errors"

	gyaml "github.com/ghodss/yaml"
	"github.com/howeyc/fsnotify"
	"github.com/polarismesh/polaris-controller/common"
	"github.com/polarismesh/polaris-controller/pkg/inject/api/annotation"
	meshconfig "github.com/polarismesh/polaris-controller/pkg/inject/api/mesh/v1alpha1"
	utils "github.com/polarismesh/polaris-controller/pkg/util"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
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
	defaultSidecarMode         utils.SidecarMode
	mu                         sync.RWMutex
	sidecarMeshConfig          *Config
	sidecarMeshTemplateVersion string
	sidecarDnsConfig           *Config
	sidecarDnsTemplateVersion  string
	meshConfig                 *meshconfig.MeshConfig
	valuesConfig               string

	healthCheckInterval time.Duration
	healthCheckFile     string

	server         *http.Server
	meshFile       string
	meshConfigFile string
	dnsConfigFile  string
	valuesFile     string
	watcher        *fsnotify.Watcher
	certFile       string
	keyFile        string
	cert           *tls.Certificate

	k8sClient kubernetes.Interface
}

// env will be used for other things besides meshConfig - when webhook is running in Istiod it can take advantage
// of the config and endpoint cache.
//nolint directives: interfacer
func loadConfig(injectMeshFile, injectDnsFile, meshFile, valuesFile string) (*Config, *Config, *meshconfig.MeshConfig, string, error) {
	// 处理 polaris-sidecar mesh 模式的注入
	meshData, err := ioutil.ReadFile(injectMeshFile)
	if err != nil {
		return nil, nil, nil, "", err
	}
	var meshConf Config
	if err := gyaml.Unmarshal(meshData, &meshConf); err != nil {
		log.Warnf("Failed to parse injectFile %s", string(meshData))
		return nil, nil, nil, "", err
	}

	// 处理 polaris-sidecar dns 模式的注入
	dnsData, err := ioutil.ReadFile(injectDnsFile)
	if err != nil {
		return nil, nil, nil, "", err
	}
	var dnsConf Config
	if err := gyaml.Unmarshal(dnsData, &dnsConf); err != nil {
		log.Warnf("Failed to parse injectFile %s", string(dnsData))
		return nil, nil, nil, "", err
	}

	valuesConfig, err := ioutil.ReadFile(valuesFile)
	if err != nil {
		return nil, nil, nil, "", err
	}

	meshConfig, err := mesh.ReadMeshConfig(meshFile)
	if err != nil {
		return nil, nil, nil, "", err
	}

	log.Infof("[MESH] New inject configuration: sha256sum %x", sha256.Sum256(meshData))
	log.Infof("[MESH] Policy: %v", meshConf.Policy)
	log.Infof("[MESH] AlwaysInjectSelector: %v", meshConf.AlwaysInjectSelector)
	log.Infof("[MESH] NeverInjectSelector: %v", meshConf.NeverInjectSelector)
	log.Infof("[MESH] Template: |\n  %v", strings.Replace(meshConf.Template, "\n", "\n  ", -1))

	log.Infof("[DNS] New inject configuration: sha256sum %x", sha256.Sum256(dnsData))
	log.Infof("[DNS] Policy: %v", dnsConf.Policy)
	log.Infof("[DNS] AlwaysInjectSelector: %v", dnsConf.AlwaysInjectSelector)
	log.Infof("[DNS] NeverInjectSelector: %v", dnsConf.NeverInjectSelector)
	log.Infof("[DNS] Template: |\n  %v", strings.Replace(dnsConf.Template, "\n", "\n  ", -1))

	return &meshConf, &dnsConf, meshConfig, string(valuesConfig), nil
}

// WebhookParameters configures parameters for the sidecar injection
// webhook.
type WebhookParameters struct {
	// DefaultSidecarMode polaris-sidecar 默认的运行模式
	DefaultSidecarMode utils.SidecarMode

	// MeshConfigFile 处理 polaris-sidecar 运行模式为 mesh 的配置文件
	MeshConfigFile string

	// DnsConfigFile 处理 polaris-sidecar 运行模式为 dns 的配置文件
	DnsConfigFile string

	ValuesFile string

	// MeshFile is the path to the mesh configuration file.
	MeshFile string

	// CertFile is the path to the x509 certificate for https.
	CertFile string

	// KeyFile is the path to the x509 private key matching `CertFile`.
	KeyFile string

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
	// TODO: pass a pointer to mesh config from Pilot bootstrap, no need to watch and load 2 times
	// This is needed before we implement advanced merging / patching of mesh config
	sidecarMeshConfig, sidecarDnsConfig, meshConfig, valuesConfig, err := loadConfig(p.MeshConfigFile, p.DnsConfigFile, p.MeshFile, p.ValuesFile)
	if err != nil {
		return nil, err
	}
	pair, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// watch the parent directory of the target files so we can catch
	// symlink updates of k8s ConfigMaps volumes.
	for _, file := range []string{p.MeshConfigFile, p.DnsConfigFile, p.MeshFile, p.CertFile, p.KeyFile} {
		if file == p.MeshFile {
			continue
		}
		watchDir, _ := filepath.Split(file)
		if err := watcher.Watch(watchDir); err != nil {
			return nil, fmt.Errorf("could not watch %v: %v", file, err)
		}
	}

	wh := &Webhook{
		sidecarMeshConfig:          sidecarMeshConfig,
		sidecarMeshTemplateVersion: sidecarTemplateVersionHash(sidecarMeshConfig.Template),
		sidecarDnsConfig:           sidecarDnsConfig,
		sidecarDnsTemplateVersion:  sidecarTemplateVersionHash(sidecarDnsConfig.Template),
		meshConfig:                 meshConfig,
		meshConfigFile:             p.MeshConfigFile,
		dnsConfigFile:              p.DnsConfigFile,
		valuesFile:                 p.ValuesFile,
		valuesConfig:               valuesConfig,
		meshFile:                   p.MeshFile,
		watcher:                    watcher,
		healthCheckInterval:        p.HealthCheckInterval,
		healthCheckFile:            p.HealthCheckFile,
		certFile:                   p.CertFile,
		keyFile:                    p.KeyFile,
		cert:                       &pair,

		// 新增查询 k8s 资源的操作者
		k8sClient:          p.Client,
		defaultSidecarMode: p.DefaultSidecarMode,
	}

	var mux *http.ServeMux
	if p.Mux != nil {
		p.Mux.HandleFunc("/inject", wh.serveInject)
		mux = p.Mux
	} else {
		wh.server = &http.Server{
			Addr: fmt.Sprintf(":%v", p.Port),
			// mtls disabled because apiserver webhook cert usage is still TBD.
			TLSConfig: &tls.Config{GetCertificate: wh.getCert},
		}
		mux = http.NewServeMux()
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
				log.Fatalf("admission webhook ListenAndServeTLS failed: %v", err)
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
			sidecarMeshConfig, sidecarDnsConfig, meshConfig, valuesConfig, err := loadConfig(wh.meshConfigFile, wh.dnsConfigFile, wh.meshFile, wh.valuesFile)
			if err != nil {
				log.Errorf("update error: %v", err)
				break
			}

			pair, err := tls.LoadX509KeyPair(wh.certFile, wh.keyFile)
			if err != nil {
				log.Errorf("reload cert error: %v", err)
				break
			}
			wh.mu.Lock()
			wh.sidecarMeshConfig = sidecarMeshConfig
			wh.sidecarMeshTemplateVersion = sidecarTemplateVersionHash(sidecarMeshConfig.Template)
			wh.sidecarDnsConfig = sidecarDnsConfig
			wh.sidecarDnsTemplateVersion = sidecarTemplateVersionHash(sidecarDnsConfig.Template)

			wh.valuesConfig = valuesConfig
			wh.meshConfig = meshConfig
			wh.cert = &pair
			wh.mu.Unlock()
		case event := <-wh.watcher.Event:
			log.Infof("Injector watch update: %+v", event)
			// use a timer to debounce configuration updates
			if (event.IsModify() || event.IsCreate()) && timerC == nil {
				timerC = time.After(watchDebounceDelay)
			}
		case err := <-wh.watcher.Error:
			log.Errorf("Watcher error: %v", err)
		case <-healthC:
			content := []byte(`ok`)
			if err := ioutil.WriteFile(wh.healthCheckFile, content, 0o644); err != nil {
				log.Errorf("Health check update of %q failed: %v", wh.healthCheckFile, err)
			}
		case <-stop:
			return
		}
	}
}

func (wh *Webhook) getCert(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	wh.mu.Lock()
	defer wh.mu.Unlock()
	return wh.cert, nil
}

// It would be great to use https://github.com/mattbaird/jsonpatch to
// generate RFC6902 JSON patches. Unfortunately, it doesn't produce
// correct patches for object removal. Fortunately, our patching needs
// are fairly simple so generating them manually isn't horrible (yet).
type rfc6902PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// JSONPatch `remove` is applied sequentially. Remove items in reverse
// order to avoid renumbering indices.
func removeContainers(containers []corev1.Container, removed []string, path string) (patch []rfc6902PatchOperation) {
	names := map[string]bool{}
	for _, name := range removed {
		names[name] = true
	}
	for i := len(containers) - 1; i >= 0; i-- {
		if _, ok := names[containers[i].Name]; ok {
			patch = append(patch, rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func removeVolumes(volumes []corev1.Volume, removed []string, path string) (patch []rfc6902PatchOperation) {
	names := map[string]bool{}
	for _, name := range removed {
		names[name] = true
	}
	for i := len(volumes) - 1; i >= 0; i-- {
		if _, ok := names[volumes[i].Name]; ok {
			patch = append(patch, rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func removeImagePullSecrets(imagePullSecrets []corev1.LocalObjectReference, removed []string, path string) (patch []rfc6902PatchOperation) {
	names := map[string]bool{}
	for _, name := range removed {
		names[name] = true
	}
	for i := len(imagePullSecrets) - 1; i >= 0; i-- {
		if _, ok := names[imagePullSecrets[i].Name]; ok {
			patch = append(patch, rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func (wh *Webhook) addContainer(sidecarMode utils.SidecarMode, pod *corev1.Pod, target, added []corev1.Container, basePath string) (patch []rfc6902PatchOperation) {
	saJwtSecretMountName := ""
	var saJwtSecretMount corev1.VolumeMount
	// find service account secret volume mount(/var/run/secrets/kubernetes.io/serviceaccount,
	// https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/#service-account-automation) from app container
	for _, add := range target {
		for _, vmount := range add.VolumeMounts {
			if vmount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
				saJwtSecretMountName = vmount.Name
				saJwtSecretMount = vmount
			}
		}
	}
	first := len(target) == 0
	var value interface{}
	for _, add := range added {
		if add.Name == ProxyContainerName && saJwtSecretMountName != "" {
			// add service account secret volume mount(/var/run/secrets/kubernetes.io/serviceaccount,
			// https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/#service-account-automation) to istio-proxy container,
			// so that envoy could fetch/pass k8s sa jwt and pass to sds server, which will be used to request workload identity for the pod.
			add.VolumeMounts = append(add.VolumeMounts, saJwtSecretMount)
		}

		// mesh condition || dns condition
		//if add.Name == "polaris-bootstrap-writer" || add.Name == "polaris-sidecar-init" {
		//	log.Infof("begin to add polaris-sidecar config to int container for pod[%s, %s]",
		//		pod.Namespace, pod.Name)
		//	if err := wh.addPolarisConfigToInitContainerEnv(&add); err != nil {
		//		log.Errorf("begin to add polaris-sidecar config to init container for pod[%s, %s] failed: %v",
		//			pod.Namespace, pod.Name, err)
		//	}
		//}

		if add.Name == "polaris-sidecar" {
			log.Infof("begin deal polaris-sidecar inject for pod=[%s, %s]", pod.Namespace, pod.Name)
			//if _, err := wh.handlePolarisSideInject(sidecarMode, pod, &add); err != nil {
			//	log.Errorf("handle polaris-sidecar inject for pod=[%s, %s] failed: %v", pod.Namespace, pod.Name, err)
			//}
			if _, err := wh.handlePolarisSidecarEnvInject(sidecarMode, pod, &add); err != nil {
				log.Errorf("handle polaris-sidecar inject for pod=[%s, %s] failed: %v", pod.Namespace, pod.Name, err)
			}

			// 将刚刚创建好的配置文件挂载到 pod 的 container 中去
			//add.VolumeMounts = append(add.VolumeMounts, corev1.VolumeMount{
			//	Name:      utils.PolarisGoConfigFile,
			//	SubPath:   "polaris.yaml",
			//	MountPath: "/data/polaris.yaml",
			//})
		}

		value = add
		path := basePath
		if first {
			first = false
			value = []corev1.Container{add}
		} else {
			path += "/-"
		}
		patch = append(patch, rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

type PolarisGoConfig struct {
	Name          string
	Namespace     string
	PolarisServer string
}

func enableMtls(pod *corev1.Pod) bool {
	value, ok := pod.Annotations[utils.PolarisTLSMode]
	if ok && value != utils.MTLSModeNone {
		return true
	}
	return false
}

func (wh *Webhook) handlePolarisSidecarEnvInject(
	sidecarMode utils.SidecarMode, pod *corev1.Pod, add *corev1.Container) (bool, error) {
	err := wh.ensureRootCertExist(pod)
	if err != nil {
		return false, err
	}
	envMap := make(map[string]string)
	envMap[EnvSidecarPort] = strconv.Itoa(ValueListenPort)
	envMap[EnvSidecarRecurseEnable] = strconv.FormatBool(true)
	if sidecarMode == utils.SidecarForDns {
		envMap[EnvSidecarDnsEnable] = strconv.FormatBool(true)
		envMap[EnvSidecarMeshEnable] = strconv.FormatBool(false)
	} else {
		envMap[EnvSidecarDnsEnable] = strconv.FormatBool(false)
		envMap[EnvSidecarMeshEnable] = strconv.FormatBool(true)
	}
	envMap[EnvSidecarLogLevel] = "info"
	envMap[EnvSidecarNamespace] = pod.GetNamespace()
	envMap[EnvPolarisAddress] = common.PolarisServerGrpcAddress
	envMap[EnvSidecarDnsRouteLabels] = buildLabelsStr(pod.Labels)
	if enableMtls(pod) {
		envMap[EnvSidecarMtlsEnable] = strconv.FormatBool(true)
	}
	log.Infof("pod=[%s, %s] inject polaris-sidecar mode %s, env map %v",
		pod.Namespace, pod.Name, utils.ParseSidecarModeName(sidecarMode), envMap)
	for k, v := range envMap {
		add.Env = append(add.Env, corev1.EnvVar{Name: k, Value: v})
	}
	return true, nil
}

func buildLabelsStr(labels map[string]string) string {
	tags := make([]string, 0, len(labels))

	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}

	return strings.Join(tags, ",")
}

// currently we assume that polaris-security deploy into polaris-system namespace.
const rootNamespace = "polaris-system"

// ensureRootCertExist ensure that we have rootca pem secret in current namespace
func (wh *Webhook) ensureRootCertExist(pod *corev1.Pod) error {
	if !enableMtls(pod) {
		return nil
	}
	ns := pod.Namespace
	_, err := wh.k8sClient.CoreV1().Secrets(ns).Get(utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}
	secret, err := wh.k8sClient.CoreV1().Secrets(rootNamespace).Get(utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// copy all data from root namespace rootca secret.
	s := &corev1.Secret{}
	s.Data = secret.Data
	s.StringData = secret.StringData
	s.Name = utils.PolarisSidecarRootCert
	_, err = wh.k8sClient.CoreV1().Secrets(ns).Create(s)
	if errors.IsAlreadyExists(err) || errors.IsConflict(err) {
		return nil
	}
	return err
}

func addSecurityContext(target *corev1.PodSecurityContext, basePath string) (patch []rfc6902PatchOperation) {
	patch = append(patch, rfc6902PatchOperation{
		Op:    "add",
		Path:  basePath,
		Value: target,
	})
	return patch
}

func addVolume(target, added []corev1.Volume, basePath string) (patch []rfc6902PatchOperation) {
	first := len(target) == 0
	var value interface{}
	for _, add := range added {
		value = add
		path := basePath
		if first {
			first = false
			value = []corev1.Volume{add}
		} else {
			path += "/-"
		}
		patch = append(patch, rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func addImagePullSecrets(target, added []corev1.LocalObjectReference, basePath string) (patch []rfc6902PatchOperation) {
	first := len(target) == 0
	var value interface{}
	for _, add := range added {
		value = add
		path := basePath
		if first {
			first = false
			value = []corev1.LocalObjectReference{add}
		} else {
			path += "/-"
		}
		patch = append(patch, rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func addPodDNSConfig(target *corev1.PodDNSConfig, basePath string) (patch []rfc6902PatchOperation) {
	patch = append(patch, rfc6902PatchOperation{
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

// adds labels to the target spec, will not overwrite label's value if it already exists
func addLabels(target map[string]string, added map[string]string) []rfc6902PatchOperation {
	patches := []rfc6902PatchOperation{}

	addedKeys := make([]string, 0, len(added))
	for key := range added {
		addedKeys = append(addedKeys, key)
	}
	sort.Strings(addedKeys)

	for _, key := range addedKeys {
		value := added[key]
		patch := rfc6902PatchOperation{
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

func updateAnnotation(target map[string]string, added map[string]string) (patch []rfc6902PatchOperation) {
	// To ensure deterministic patches, we sort the keys
	var keys []string
	for k := range added {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := added[key]
		if target == nil {
			target = map[string]string{}
			patch = append(patch, rfc6902PatchOperation{
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
			patch = append(patch, rfc6902PatchOperation{
				Op:    op,
				Path:  "/metadata/annotations/" + escapeJSONPointerValue(key),
				Value: value,
			})
		}
	}
	return patch
}

func (wh *Webhook) createPatch(sidecarMode utils.SidecarMode, pod *corev1.Pod, prevStatus *SidecarInjectionStatus, annotations map[string]string, sic *SidecarInjectionSpec,
	workloadName string,
) ([]byte, error) {
	var patch []rfc6902PatchOperation

	// Remove any containers previously injected by kube-inject using
	// container and volume name as unique key for removal.
	patch = append(patch, removeContainers(pod.Spec.InitContainers, prevStatus.InitContainers, "/spec/initContainers")...)
	patch = append(patch, removeContainers(pod.Spec.Containers, prevStatus.Containers, "/spec/containers")...)
	patch = append(patch, removeVolumes(pod.Spec.Volumes, prevStatus.Volumes, "/spec/volumes")...)
	patch = append(patch, removeImagePullSecrets(pod.Spec.ImagePullSecrets, prevStatus.ImagePullSecrets, "/spec/imagePullSecrets")...)

	patch = append(patch, wh.addContainer(sidecarMode, pod, pod.Spec.InitContainers, sic.InitContainers, "/spec/initContainers")...)
	patch = append(patch, wh.addContainer(sidecarMode, pod, pod.Spec.Containers, sic.Containers, "/spec/containers")...)
	patch = append(patch, addVolume(pod.Spec.Volumes, sic.Volumes, "/spec/volumes")...)
	patch = append(patch, addImagePullSecrets(pod.Spec.ImagePullSecrets, sic.ImagePullSecrets, "/spec/imagePullSecrets")...)

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
	legacyInitContainerNames = []string{"istio-init", "enable-core-dump"}
	legacyContainerNames     = []string{ProxyContainerName}
	legacyVolumeNames        = []string{"polaris-certs", "polaris-envoy"}
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
		// heuristic assumes status is valid if any of the resource
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
	return &v1beta1.AdmissionResponse{Result: &metav1.Status{Message: err.Error()}}
}

func (wh *Webhook) injectV1beta1(ar *v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	req := ar.Request
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		handleError(fmt.Sprintf("Could not unmarshal raw object: %v %s", err,
			string(req.Object.Raw)))
		return toV1beta1AdmissionResponse(err)
	}

	sidecarMode := wh.getSidecarMode(req.Namespace)

	// Deal with potential empty fields, e.g., when the pod is created by a deployment
	podName := potentialPodName(&pod.ObjectMeta)
	if pod.ObjectMeta.Namespace == "" {
		pod.ObjectMeta.Namespace = req.Namespace
	}
	if len(pod.ObjectMeta.Annotations) == 0 {
		pod.ObjectMeta.Annotations = make(map[string]string)
	}

	log.Infof("AdmissionReview for Kind=%v Namespace=%v Name=%v (%v) UID=%v Rfc6902PatchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, podName, req.UID, req.Operation, req.UserInfo)
	log.Infof("Object: %v", string(req.Object.Raw))
	log.Infof("OldObject: %v", string(req.OldObject.Raw))

	config := wh.sidecarMeshConfig
	tempVersion := wh.sidecarMeshTemplateVersion
	if sidecarMode == utils.SidecarForDns {
		config = wh.sidecarDnsConfig
		tempVersion = wh.sidecarDnsTemplateVersion
	}

	if !wh.injectRequired(ignoredNamespaces, config, &pod.Spec, &pod.ObjectMeta) {
		log.Infof("Skipping %s/%s due to policy check", pod.ObjectMeta.Namespace, podName)
		return &v1beta1.AdmissionResponse{
			Allowed: true,
		}
	}

	// due to bug https://github.com/kubernetes/kubernetes/issues/57923,
	// k8s sa jwt token volume mount file is only accessible to root user, not istio-proxy(the user that istio proxy runs as).
	// workaround by https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#set-the-security-context-for-a-pod
	if wh.meshConfig.SdsUdsPath != "" {
		grp := int64(1337)
		if pod.Spec.SecurityContext == nil {
			pod.Spec.SecurityContext = &corev1.PodSecurityContext{
				FSGroup: &grp,
			}
		} else {
			pod.Spec.SecurityContext.FSGroup = &grp
		}
	}

	// try to capture more useful namespace/name info for deployments, etc.
	// TODO(dougreid): expand to enable lookup of OWNERs recursively a la kubernetesenv
	deployMeta := pod.ObjectMeta.DeepCopy()
	deployMeta.Namespace = req.Namespace

	typeMetadata := &metav1.TypeMeta{
		Kind:       "Pod",
		APIVersion: "v1",
	}

	if len(pod.GenerateName) > 0 {
		// if the pod name was generated (or is scheduled for generation), we can begin an investigation into the controlling reference for the pod.
		var controllerRef metav1.OwnerReference
		controllerFound := false
		for _, ref := range pod.GetOwnerReferences() {
			if *ref.Controller {
				controllerRef = ref
				controllerFound = true
				break
			}
		}
		if controllerFound {
			typeMetadata.APIVersion = controllerRef.APIVersion
			typeMetadata.Kind = controllerRef.Kind

			// heuristic for deployment detection
			if typeMetadata.Kind == "ReplicaSet" && strings.HasSuffix(controllerRef.Name, pod.Labels["pod-template-hash"]) {
				name := strings.TrimSuffix(controllerRef.Name, "-"+pod.Labels["pod-template-hash"])
				deployMeta.Name = name
				typeMetadata.Kind = "Deployment"
			} else {
				deployMeta.Name = controllerRef.Name
			}
		}
	}

	if deployMeta.Name == "" {
		// if we haven't been able to extract a deployment name, then just give it the pod name
		deployMeta.Name = pod.Name
	}
	proxyCfg := wh.meshConfig.DefaultConfig
	spec, iStatus, err := InjectionData(config.Template, wh.valuesConfig, tempVersion, typeMetadata, deployMeta, &pod.Spec, &pod.ObjectMeta, proxyCfg, wh.meshConfig) // nolint: lll
	if err != nil {
		handleError(fmt.Sprintf("Injection data: err=%v spec=%v\n", err, iStatus))
		return toV1beta1AdmissionResponse(err)
	}

	annotations := map[string]string{}
	if len(iStatus) != 0 {
		annotations[annotation.SidecarStatus.Name] = iStatus
	}

	// Add all additional injected annotations
	for k, v := range config.InjectedAnnotations {
		annotations[k] = v
	}

	patchBytes, err := wh.createPatch(sidecarMode, &pod, injectionStatus(&pod), annotations, spec, deployMeta.Name)
	if err != nil {
		handleError(fmt.Sprintf("AdmissionResponse: err=%v spec=%v\n", err, spec))
		return toV1beta1AdmissionResponse(err)
	}

	log.Infof("AdmissionResponse: patch=%v\n", string(patchBytes))

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
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		handleError(fmt.Sprintf("Could not unmarshal raw object: %v %s", err,
			string(req.Object.Raw)))
		return toV1AdmissionResponse(err)
	}

	sidecarMode := wh.getSidecarMode(req.Namespace)

	// Deal with potential empty fields, e.g., when the pod is created by a deployment
	podName := potentialPodName(&pod.ObjectMeta)
	if pod.ObjectMeta.Namespace == "" {
		pod.ObjectMeta.Namespace = req.Namespace
	}
	if len(pod.ObjectMeta.Annotations) == 0 {
		pod.ObjectMeta.Annotations = make(map[string]string)
	}

	log.Infof("[Webhook] admissionReview for Kind=%v Namespace=%v Name=%v (%v) UID=%v Rfc6902PatchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, podName, req.UID, req.Operation, req.UserInfo)
	log.Infof("[Webhook] object: %v", string(req.Object.Raw))
	log.Infof("[Webhook] oldObject: %v", string(req.OldObject.Raw))

	config := wh.sidecarMeshConfig
	tempVersion := wh.sidecarMeshTemplateVersion
	if sidecarMode == utils.SidecarForDns {
		config = wh.sidecarDnsConfig
		tempVersion = wh.sidecarDnsTemplateVersion
	}

	if !wh.injectRequired(ignoredNamespaces, config, &pod.Spec, &pod.ObjectMeta) {
		log.Infof("[Webhook] skipping %s/%s due to policy check", pod.ObjectMeta.Namespace, podName)
		return &v1.AdmissionResponse{
			Allowed: true,
		}
	}

	// due to bug https://github.com/kubernetes/kubernetes/issues/57923,
	// k8s sa jwt token volume mount file is only accessible to root user, not istio-proxy(the user that istio proxy runs as).
	// workaround by https://kubernetes.io/docs/tasks/configure-pod-container/security-context/#set-the-security-context-for-a-pod
	if wh.meshConfig.SdsUdsPath != "" {
		grp := int64(1337)
		if pod.Spec.SecurityContext == nil {
			pod.Spec.SecurityContext = &corev1.PodSecurityContext{
				FSGroup: &grp,
			}
		} else {
			pod.Spec.SecurityContext.FSGroup = &grp
		}
	}

	// try to capture more useful namespace/name info for deployments, etc.
	// TODO(dougreid): expand to enable lookup of OWNERs recursively a la kubernetesenv
	deployMeta := pod.ObjectMeta.DeepCopy()
	deployMeta.Namespace = req.Namespace

	typeMetadata := &metav1.TypeMeta{
		Kind:       "Pod",
		APIVersion: "v1",
	}

	if len(pod.GenerateName) > 0 {
		// if the pod name was generated (or is scheduled for generation), we can begin an investigation into the controlling reference for the pod.
		var controllerRef metav1.OwnerReference
		controllerFound := false
		for _, ref := range pod.GetOwnerReferences() {
			if *ref.Controller {
				controllerRef = ref
				controllerFound = true
				break
			}
		}
		if controllerFound {
			typeMetadata.APIVersion = controllerRef.APIVersion
			typeMetadata.Kind = controllerRef.Kind

			// heuristic for deployment detection
			if typeMetadata.Kind == "ReplicaSet" && strings.HasSuffix(controllerRef.Name, pod.Labels["pod-template-hash"]) {
				name := strings.TrimSuffix(controllerRef.Name, "-"+pod.Labels["pod-template-hash"])
				deployMeta.Name = name
				typeMetadata.Kind = "Deployment"
			} else {
				deployMeta.Name = controllerRef.Name
			}
		}
	}

	if deployMeta.Name == "" {
		// if we haven't been able to extract a deployment name, then just give it the pod name
		deployMeta.Name = pod.Name
	}

	spec, iStatus, err := InjectionData(config.Template, wh.valuesConfig, tempVersion, typeMetadata, deployMeta, &pod.Spec, &pod.ObjectMeta, wh.meshConfig.DefaultConfig, wh.meshConfig) // nolint: lll
	if err != nil {
		handleError(fmt.Sprintf("Injection data: err=%v spec=%v\n", err, iStatus))
		return toV1AdmissionResponse(err)
	}

	annotations := map[string]string{}
	if len(iStatus) != 0 {
		annotations[annotation.SidecarStatus.Name] = iStatus
	}

	// Add all additional injected annotations
	for k, v := range config.InjectedAnnotations {
		annotations[k] = v
	}

	patchBytes, err := wh.createPatch(sidecarMode, &pod, injectionStatus(&pod), annotations, spec, deployMeta.Name)
	if err != nil {
		handleError(fmt.Sprintf("AdmissionResponse: err=%v spec=%v\n", err, spec))
		return toV1AdmissionResponse(err)
	}

	log.Infof("[Webhook] admissionResponse: patch=%v\n", string(patchBytes))

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
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}
	log.Infof("[Webhook] receive webhook request path %s, data %s", r.URL.RawPath, string(body))
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
			log.Errorf("[Webhook] expected v1beta1.AdmissionReview but got: %T", obj)
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
			log.Errorf("[Webhook] expected v1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &v1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = wh.injectV1(requestAdmissionReview)
		responseAdmissionReview.Response.UID = requestAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	default:
		msg := fmt.Sprintf("[Webhook] unsupported group version kind: %v", gvk)
		log.Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
	}

	resp, err := json.Marshal(responseObj)
	if err != nil {
		log.Errorf("Could not encode response: %v", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	if _, err := w.Write(resp); err != nil {
		log.Errorf("Could not write response: %v", err)
		http.Error(w, fmt.Sprintf("could not write response: %v", err), http.StatusInternalServerError)
	}
}

func handleError(message string) {
	log.Errorf(message)
}
