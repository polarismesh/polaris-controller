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

package javaagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/base"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

// Java Agent 场景下的特殊 annonations 信息
const (
	envJavaAgentPluginType       = "JAVA_AGENT_PLUGIN_TYPE"
	envJavaAgentFrameworkName    = "JAVA_AGENT_FRAMEWORK_NAME"
	envJavaAgentFrameworkVersion = "JAVA_AGENT_FRAMEWORK_VERSION"
)

var oldAgentVersions = map[string]struct{}{
	"1.7.0-RC5": {},
	"1.7.0-RC4": {},
	"1.7.0-RC3": {},
	"1.7.0-RC2": {},
	"1.7.0-RC1": {},
	"1.6.1":     {},
	"1.6.0":     {},
}

const (
	ActiveJavaAgentCmd    = "-javaagent:/app/lib/.polaris/java_agent/polaris-java-agent/polaris-agent-core-bootstrap.jar"
	OldActiveJavaAgentCmd = "-javaagent:/app/lib/.polaris/java_agent/polaris-java-agent-%s/polaris-agent-core-bootstrap.jar"

	javaagentInitContainer = "polaris-javaagent-init"
)

func init() {
	inject.RegisterPatchBuilder(utils.SidecarJavaAgentModeName, &PodPatchBuilder{})
}

// PodPatchBuilder
type PodPatchBuilder struct {
	*base.PodPatchBuilder
}

func (pb *PodPatchBuilder) PatchContainer(req *inject.OperateContainerRequest) ([]inject.Rfc6902PatchOperation, error) {
	switch req.Type {
	case inject.PatchType_Remove:
		return pb.PodPatchBuilder.PatchContainer(req)
	case inject.PatchType_Add:
		pod := req.Option.Pod
		added := req.External
		for index, add := range added {
			if add.Name == javaagentInitContainer {
				log.InjectScope().Infof("begin deal polaris-javaagent-init inject for pod=[%s, %s]", pod.Namespace, pod.Name)
				if err := pb.handleJavaAgentInit(req, &add); err != nil {
					log.InjectScope().Errorf("handle polaris-javaagent-init inject for pod=[%s, %s] failed: %v", pod.Namespace, pod.Name, err)
				}
			}
			added[index] = add
		}
		// 重新更新请求参数中的 req.External
		req.External = added
		log.InjectScope().Infof("finish deal polaris-javaagent-init inject for pod=[%s, %s] added: %#v", pod.Namespace, pod.Name, added)
		return pb.PodPatchBuilder.PatchContainer(req)
	case inject.PatchType_Update:
		return pb.updateContainer(req), nil
	}
	return nil, nil
}

func (pb *PodPatchBuilder) handleJavaAgentInit(req *inject.OperateContainerRequest, add *corev1.Container) error {
	pod := req.Option.Pod
	opt := req.Option
	annonations := pod.Annotations
	log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] annonations: %#v image: %s",
		pod.Namespace, pod.Name, pod.Annotations, add.Image)
	// 判断用户是否自定义了 javaagent 的版本
	oldImageInfo := strings.Split(add.Image, ":")
	if len(oldImageInfo) > 1 {
		opt.ExternalInfo[utils.AnnotationKeyJavaAgentVersion] = oldImageInfo[1]
	}
	if val, ok := annonations[utils.AnnotationKeyJavaAgentVersion]; ok && val != "" {
		add.Image = fmt.Sprintf("%s:%s", oldImageInfo[0], val)
		opt.ExternalInfo[utils.AnnotationKeyJavaAgentVersion] = val
	}

	frameworkName, frameworkNameOk := annonations[utils.AnnotationKeyJavaAgentPluginFramework]
	if !frameworkNameOk {
		log.InjectScope().Warnf("handle polaris-javaagent-init inject for pod=[%s, %s] not found frameworkName",
			pod.Namespace, pod.Name)
	}

	frameworkVersion, frameworkVersionOk := annonations[utils.AnnotationKeyJavaAgentPluginFrameworkVersion]
	if !frameworkVersionOk {
		log.InjectScope().Warnf("handle polaris-javaagent-init inject for pod=[%s, %s] not found frameworkVersion",
			pod.Namespace, pod.Name)
	}

	pluginType := frameworkName + frameworkVersion
	if frameworkName != "" && frameworkVersion != "" {
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  envJavaAgentPluginType,
			Value: pluginType,
		})
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  envJavaAgentFrameworkName,
			Value: frameworkName,
		})
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  envJavaAgentFrameworkVersion,
			Value: frameworkVersion,
		})
	}

	defaultParam := map[string]string{
		"MicroserviceNamespace": getServiceNamespace(opt),
		"MicroserviceName":      opt.Annotations[util.SidecarServiceName],
		"PolarisServerIP":       strings.Split(polarisapi.PolarisGrpc, ":")[0],
		"PolarisDiscoverPort":   strings.Split(polarisapi.PolarisGrpc, ":")[1],
		"PolarisConfigIP":       strings.Split(polarisapi.PolarisConfigGrpc, ":")[0],
		"PolarisConfigPort":     strings.Split(polarisapi.PolarisConfigGrpc, ":")[1],
	}

	add.Env = append(add.Env,
		corev1.EnvVar{
			Name:  "POLARIS_SERVER_IP",
			Value: defaultParam["PolarisServerIP"],
		},
		corev1.EnvVar{
			Name:  "POLARIS_DISCOVER_IP",
			Value: defaultParam["PolarisServerIP"],
		},
		corev1.EnvVar{
			Name:  "POLARIS_DISCOVER_PORT",
			Value: defaultParam["PolarisDiscoverPort"],
		},
		corev1.EnvVar{
			Name:  "POLARIS_CONFIG_IP",
			Value: defaultParam["PolarisConfigIP"],
		},
		corev1.EnvVar{
			Name:  "POLARIS_CONFIG_PORT",
			Value: defaultParam["PolarisConfigPort"],
		},
	)
	defaultProperties := make(map[string]string)
	// 判断是不是老版本，如果是老版本且客户填写的版本号不为空则走老的逻辑，否则走新的逻辑，只下发北极星的地址和端口信息
	newImageInfo := strings.Split(add.Image, ":")
	// RC5之前的版本走的逻辑, 向前兼容
	if _, valid := oldAgentVersions[newImageInfo[1]]; valid {
		kubeClient := opt.KubeClient
		pluginCm, err := kubeClient.CoreV1().ConfigMaps(util.RootNamespace).Get(context.Background(),
			"plugin-default.properties", metav1.GetOptions{})
		if err != nil {
			return err
		}
		tpl, err := template.New(pluginType).Parse(pluginCm.Data[nameOfPluginDefault(pluginType)])
		if err != nil {
			return err
		}
		buf := new(bytes.Buffer)
		if err := tpl.Execute(buf, defaultParam); err != nil {
			return err
		}
		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		scanner.Split(bufio.ScanLines)
		for scanner.Scan() {
			line := scanner.Text()
			// 注释不放在 defaultProperties 中
			if !strings.HasPrefix(line, "#") {
				kvs := strings.Split(line, "=")
				if len(kvs) == 2 && kvs[0] != "" && kvs[1] != "" {
					defaultProperties[strings.TrimSpace(kvs[0])] = strings.TrimSpace(kvs[1])
				}
			}
		}
		// 查看用户是否自定义了相关配置信息, 优先级最高
		// 需要根据用户的自定义参数信息，将 agent 的特定 application.properties 文件注入到 javaagent-init 中
		if properties, ok := annonations[utils.AnnotationKeyJavaAgentPluginConfig]; ok {
			customProperties := map[string]string{}
			if err := json.Unmarshal([]byte(properties), &customProperties); err != nil {
				return err
			}
			// 先从 configmap 中获取 java-agent 不同 plugin-type 的默认配置信息
			for k, v := range customProperties {
				if defaultValue, defaultExist := defaultProperties[k]; defaultExist {
					if defaultValue != v {
						log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] "+
							"customProperties[%s]=%s should replace defaultProperties[%s]=%s",
							pod.Namespace, pod.Name, k, v, k, defaultValue)
					}
				}
				// 当和默认初始化配置存在冲突时，使用用户自定义配置
				defaultProperties[k] = v
			}
		}

		exportAgentPluginConf := ""
		for key, value := range defaultProperties {
			exportAgentPluginConf += fmt.Sprintf("%s=%s\n", key, value)
		}

		add.Env = append(add.Env, corev1.EnvVar{
			Name:  "JAVA_AGENT_PLUGIN_CONF",
			Value: exportAgentPluginConf,
		})
	}
	return nil
}

func getDefaultProperties(opt *inject.PatchOptions) map[string]string {
	defaultProperties := make(map[string]string)
	if useWorkloadNS, ok := opt.Pod.Annotations[utils.AnnotationKeyWorkloadNamespaceAsServiceNamespace]; ok &&
		useWorkloadNS == utils.InjectionValueTrue {
		log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] useWorkloadNS=%s",
			opt.Pod.Namespace, opt.Pod.Name, useWorkloadNS)
		defaultProperties[ServiceNamespaceValueFromKey] = opt.Pod.Namespace
	}
	if useWorkloadName, ok := opt.Pod.Annotations[utils.AnnotationKeyWorkloadNameAsServiceName]; ok &&
		useWorkloadName == utils.InjectionValueTrue {
		log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] useWorkloadName=%s",
			opt.Pod.Namespace, opt.Pod.Name, opt.WorkloadName)
		defaultProperties[ServiceNameValueFromKey] = opt.WorkloadName
	}
	return defaultProperties
}

func getServiceNamespace(opt *inject.PatchOptions) string {
	if useWorkloadNS, ok := opt.Pod.Annotations[utils.AnnotationKeyWorkloadNamespaceAsServiceNamespace]; ok &&
		useWorkloadNS == utils.InjectionValueTrue {
		log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] useWorkloadNS=%s",
			opt.Pod.Namespace, opt.Pod.Name, useWorkloadNS)
		return opt.Pod.Namespace
	}
	return "default"
}

func nameOfPluginDefault(v string) string {
	return v + "-default-properties"
}

func updateJavaEnvVar(envVar corev1.EnvVar, cmd string, version string) corev1.EnvVar {
	return corev1.EnvVar{
		Name:  "JAVA_TOOL_OPTIONS",
		Value: envVar.Value + " " + cmd + version,
	}
}

const (
	ServiceNameValueFromKey      = "spring.application.name"
	ServiceNamespaceValueFromKey = "spring.cloud.polaris.discovery.namespace"
)

func (pb *PodPatchBuilder) updateContainer(req *inject.OperateContainerRequest) []inject.Rfc6902PatchOperation {
	opt := req.Option
	pod := req.Option.Pod
	target := req.Option.Pod.Spec.Containers
	basePath := req.BasePath
	patches := make([]inject.Rfc6902PatchOperation, 0, len(target))

	// 判断用户是否自定义了 javaagent 的版本
	if val, ok := pod.Annotations[utils.AnnotationKeyJavaAgentVersion]; ok && val != "" {
		opt.ExternalInfo[utils.AnnotationKeyJavaAgentVersion] = val
	} else {
		pod.Annotations[utils.AnnotationKeyJavaAgentVersion] = "latest"
	}

	// 初始化默认配置和用户自定义配置
	defaultProperties := getDefaultProperties(opt)
	for index, container := range target {
		envs := container.Env
		javaEnvIndex := -1
		if properties, ok := pod.Annotations[utils.AnnotationKeyJavaAgentPluginConfig]; ok {
			customProperties := map[string]string{}
			if properties != "" {
				if err := json.Unmarshal([]byte(properties), &customProperties); err != nil {
					log.InjectScope().Errorf("updateContainer for pod=[%s, %s] json error: %+v", pod.Namespace,
						pod.Name, err)
				}
			}
			// 先从 configmap 中获取 java-agent 不同 plugin-type 的默认配置信息
			for k, v := range customProperties {
				if existsValue, exists := defaultProperties[k]; exists {
					if existsValue != v {
						log.InjectScope().Errorf("updateContainer for pod=[%s, %s] customProperties[%s]=%s, "+
							"replace defaultProperties[%s]=%s", pod.Namespace, pod.Name, k, v, k, existsValue)
					}
				}
				// 当和默认初始化配置存在冲突时，使用用户自定义配置
				defaultProperties[k] = v
			}
		}

		// 提取键到切片
		keys := make([]string, 0, len(defaultProperties))
		for k := range defaultProperties {
			keys = append(keys, k)
		}
		// 排序（按字母升序）
		sort.Strings(keys)
		// 按顺序遍历, 将配置转成-D参数, 并追加到JAVA_TOOL_OPTIONS
		var javaToolOptionsValue string
		for _, key := range keys {
			value := defaultProperties[key]
			javaToolOptionsValue += fmt.Sprintf(" -D%s=%s", key, value)
		}
		if len(envs) != 0 {
			for i := range envs {
				if envs[i].Name == "JAVA_TOOL_OPTIONS" {
					javaEnvIndex = i
					break
				}
			}
			// 环境变量 JAVA_TOOL_OPTIONS 已经存在, 往里面追加参数
			if javaEnvIndex != -1 {
				// RC5之后的版本,不再需要javaagentVersion注解,自动识别版本号
				if _, valid := oldAgentVersions[pod.Annotations[utils.AnnotationKeyJavaAgentVersion]]; !valid {
					envs[javaEnvIndex] = updateJavaEnvVar(envs[javaEnvIndex], ActiveJavaAgentCmd, javaToolOptionsValue)
				} else {
					// RC5之前的版本,需要注入javaagentVersion注解,在-javaagent参数里面指定版本号
					envs[javaEnvIndex] = updateJavaEnvVar(envs[javaEnvIndex], fmt.Sprintf(OldActiveJavaAgentCmd,
						opt.ExternalInfo[utils.AnnotationKeyJavaAgentVersion]), "")
				}
			}
		}
		// 环境变量 JAVA_TOOL_OPTIONS 不存在, 新建
		if javaEnvIndex == -1 {
			// 注入 java agent 需要用到的参数信息
			var newEnvVar corev1.EnvVar
			if _, valid := oldAgentVersions[pod.Annotations[utils.AnnotationKeyJavaAgentVersion]]; !valid {
				newEnvVar = updateJavaEnvVar(corev1.EnvVar{}, ActiveJavaAgentCmd, javaToolOptionsValue)
			} else {
				newEnvVar = updateJavaEnvVar(corev1.EnvVar{}, fmt.Sprintf(OldActiveJavaAgentCmd,
					opt.ExternalInfo[utils.AnnotationKeyJavaAgentVersion]), "")
			}
			container.Env = append(container.Env, newEnvVar)
		}

		// container 需要新挂载磁盘
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				Name:      "java-agent-dir",
				MountPath: "/app/lib/.polaris/java_agent",
			})
		path := basePath
		path += "/" + strconv.Itoa(index)
		patches = append(patches, inject.Rfc6902PatchOperation{
			Op:    "replace",
			Path:  path,
			Value: container,
		})
	}
	return patches
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
