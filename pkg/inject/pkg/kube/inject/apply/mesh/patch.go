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

package mesh

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/common"
	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/base"
	"github.com/polarismesh/polaris-controller/pkg/util"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

func init() {
	inject.RegisterPatchBuilder(utils.SidecarDnsModeName, &PodPatchBuilder{})
	inject.RegisterPatchBuilder(utils.SidecarMeshModeName, &PodPatchBuilder{})
}

// PodPatchBuilder
type PodPatchBuilder struct {
	*base.PodPatchBuilder
}

func (pb *PodPatchBuilder) PatchContainer(req *inject.OperateContainerRequest) ([]inject.Rfc6902PatchOperation, error) {
	switch req.Type {
	case inject.PatchType_Add:
		pod := req.Option.Pod
		added := req.External
		for index, add := range added {
			// mesh condition || dns condition
			if add.Name == "polaris-bootstrap-writer" || add.Name == "polaris-sidecar-init" {
				log.InjectScope().Infof("begin to add polaris-sidecar config to int container for pod[%s, %s]",
					pod.Namespace, pod.Name)
				if err := pb.addPolarisConfigToInitContainerEnv(req.Option, &add); err != nil {
					log.InjectScope().Errorf("begin to add polaris-sidecar config to init container for pod[%s, %s] failed: %v",
						pod.Namespace, pod.Name, err)
				}
			}
			if add.Name == "polaris-sidecar" {
				log.InjectScope().Infof("begin deal polaris-sidecar inject for pod=[%s, %s]", pod.Namespace, pod.Name)
				if _, err := pb.handlePolarisSidecarEnvInject(req.Option, pod, &add); err != nil {
					log.InjectScope().Errorf("handle polaris-sidecar inject for pod=[%s, %s] failed: %v", pod.Namespace, pod.Name, err)
				}
				// 将刚刚创建好的配置文件挂载到 pod 的 container 中去
				add.VolumeMounts = append(add.VolumeMounts, corev1.VolumeMount{
					Name:      utils.PolarisGoConfigFile,
					SubPath:   "polaris.yaml",
					MountPath: "/data/polaris.yaml",
				})
			}
			added[index] = add
		}
		// 重新更新请求参数中的 req.External
		req.External = added
	}
	return pb.PodPatchBuilder.PatchContainer(req)
}

func (pb *PodPatchBuilder) handlePolarisSidecarEnvInject(opt *inject.PatchOptions, pod *corev1.Pod, add *corev1.Container) (bool, error) {

	err := pb.ensureRootCertExist(opt.KubeClient, pod)
	if err != nil {
		return false, err
	}
	envMap := make(map[string]string)
	envMap[EnvSidecarPort] = strconv.Itoa(ValueListenPort)
	envMap[EnvSidecarRecurseEnable] = strconv.FormatBool(true)
	if opt.SidecarMode == utils.SidecarForDns {
		envMap[EnvSidecarDnsEnable] = strconv.FormatBool(true)
		envMap[EnvSidecarMeshEnable] = strconv.FormatBool(false)
		envMap[EnvSidecarMetricEnable] = strconv.FormatBool(false)
		envMap[EnvSidecarMetricListenPort] = strconv.Itoa(ValueMetricListenPort)
	} else {
		envMap[EnvSidecarDnsEnable] = strconv.FormatBool(false)
		envMap[EnvSidecarMeshEnable] = strconv.FormatBool(true)
		envMap[EnvSidecarRLSEnable] = strconv.FormatBool(true)
		envMap[EnvSidecarMetricEnable] = strconv.FormatBool(true)
		envMap[EnvSidecarMetricListenPort] = strconv.Itoa(ValueMetricListenPort)
	}
	envMap[EnvSidecarLogLevel] = "info"
	envMap[EnvSidecarNamespace] = pod.GetNamespace()
	envMap[EnvPolarisAddress] = common.PolarisServerGrpcAddress
	envMap[EnvSidecarDnsRouteLabels] = buildLabelsStr(pod.Labels)
	if inject.EnableMtls(pod) {
		envMap[EnvSidecarMtlsEnable] = strconv.FormatBool(true)
	}
	log.InjectScope().Infof("pod=[%s, %s] inject polaris-sidecar mode %s, env map %v",
		pod.Namespace, pod.Name, utils.ParseSidecarModeName(opt.SidecarMode), envMap)
	for k, v := range envMap {
		add.Env = append(add.Env, corev1.EnvVar{Name: k, Value: v})
	}
	return true, nil
}

// ensureRootCertExist ensure that we have rootca pem secret in current namespace
func (pb *PodPatchBuilder) ensureRootCertExist(k8sClient kubernetes.Interface, pod *corev1.Pod) error {
	if !inject.EnableMtls(pod) {
		return nil
	}
	ns := pod.Namespace
	_, err := k8sClient.CoreV1().Secrets(ns).Get(context.TODO(), utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return err
	}
	secret, err := k8sClient.CoreV1().Secrets(util.RootNamespace).Get(context.TODO(), utils.PolarisSidecarRootCert, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// copy all data from root namespace rootca secret.
	s := &corev1.Secret{}
	s.Data = secret.Data
	s.StringData = secret.StringData
	s.Name = utils.PolarisSidecarRootCert
	_, err = k8sClient.CoreV1().Secrets(ns).Create(context.TODO(), s, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) || errors.IsConflict(err) {
		return nil
	}
	return err
}

func buildLabelsStr(labels map[string]string) string {
	tags := make([]string, 0, len(labels))

	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}

	return strings.Join(tags, ",")
}

// addPolarisConfigToInitContainerEnv 将polaris-sidecar 的配置注入到init container中
func (pb *PodPatchBuilder) addPolarisConfigToInitContainerEnv(opt *inject.PatchOptions, add *corev1.Container) error {
	k8sClient := opt.KubeClient
	cfgTpl, err := k8sClient.CoreV1().ConfigMaps(common.PolarisControllerNamespace).
		Get(context.TODO(), utils.PolarisGoConfigFileTpl, metav1.GetOptions{})
	if err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}

	tmp, err := template.New("polaris-config-init").Parse(cfgTpl.Data["polaris.yaml"])
	if err != nil {
		log.InjectScope().Errorf("[Webhook][Inject] parse polaris-sidecar failed: %v", err)
		return err
	}
	buf := new(bytes.Buffer)
	if err := tmp.Execute(buf, inject.PolarisGoConfig{
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

func (pb *PodPatchBuilder) PatchVolumes(req *inject.OperateVolumesRequest) ([]inject.Rfc6902PatchOperation, error) {
	return pb.PodPatchBuilder.PatchVolumes(req)
}

func (pb *PodPatchBuilder) PatchImagePullSecrets(req *inject.OperateImagePullSecretsRequest) ([]inject.Rfc6902PatchOperation, error) {
	return pb.PodPatchBuilder.PatchImagePullSecrets(req)
}

func (pb *PodPatchBuilder) PatchSecurityContext() ([]inject.Rfc6902PatchOperation, error) {
	return nil, nil
}

func (pb *PodPatchBuilder) PatchDnsConfig() ([]inject.Rfc6902PatchOperation, error) {
	return nil, nil
}
