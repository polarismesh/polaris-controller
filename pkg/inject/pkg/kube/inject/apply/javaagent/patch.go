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
	"strconv"
	"strings"
	"text/template"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/base"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Java Agent 场景下的特殊 annonations 信息
const (
	customJavaAgentVersion                = "polarismesh.cn/javaagentVersion"
	customJavaAgentPluginFramework        = "polarismesh.cn/javaagentFrameworkName"
	customJavaAgentPluginFrameworkVersion = "polarismesh.cn/javaagentFrameworkVersion"
	customJavaAgentPluginConfig           = "polarismesh.cn/javaagentConfig"
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
			if add.Name == "polaris-javaagent-init" {
				log.InjectScope().Infof("begin deal polaris-javaagent-init inject for pod=[%s, %s]", pod.Namespace, pod.Name)
				if err := pb.handleJavaAgentInit(req.Option, pod, &add); err != nil {
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
		return pb.updateContainer(req.Option, req.Option.SidecarMode, req.Option.Pod, req.Option.Pod.Spec.Containers, req.BasePath), nil
	}
	return nil, nil
}

func (pb *PodPatchBuilder) handleJavaAgentInit(opt *inject.PatchOptions, pod *corev1.Pod, add *corev1.Container) error {
	annonations := pod.Annotations
	log.InjectScope().Infof("handle polaris-javaagent-init inject for pod=[%s, %s] annonations: %#v image: %s",
		pod.Namespace, pod.Name, pod.Annotations, add.Image)
	// 判断用户是否自定义了 javaagent 的版本
	oldImageInfo := strings.Split(add.Image, ":")
	if len(oldImageInfo) > 1 {
		opt.ExternalInfo[customJavaAgentVersion] = oldImageInfo[1]
	}
	if val, ok := annonations[customJavaAgentVersion]; ok && val != "" {
		add.Image = fmt.Sprintf("%s:%s", oldImageInfo[0], val)
		opt.ExternalInfo[customJavaAgentVersion] = val
	}

	frameworkName, frameworkNameOk := annonations[customJavaAgentPluginFramework]
	if !frameworkNameOk {
		log.InjectScope().Warnf("handle polaris-javaagent-init inject for pod=[%s, %s] not found frameworkName",
			pod.Namespace, pod.Name)
	}

	frameworkVersion, frameworkVersionOk := annonations[customJavaAgentPluginFrameworkVersion]
	if !frameworkVersionOk {
		log.InjectScope().Warnf("handle polaris-javaagent-init inject for pod=[%s, %s] not found frameworkVersion",
			pod.Namespace, pod.Name)
	}

	pluginType := frameworkName + frameworkVersion
	if frameworkName != "" && frameworkVersion != "" {
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  "JAVA_AGENT_PLUGIN_TYPE",
			Value: pluginType,
		})
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  "JAVA_AGENT_FRAMEWORK_NAME",
			Value: frameworkName,
		})
		add.Env = append(add.Env, corev1.EnvVar{
			Name:  "JAVA_AGENT_FRAMEWORK_VERSION",
			Value: frameworkVersion,
		})
	}

	defaultParam := map[string]string{
		"MicroserviceName":    opt.Annotations[util.SidecarServiceName],
		"PolarisServerIP":     strings.Split(polarisapi.PolarisGrpc, ":")[0],
		"PolarisDiscoverPort": strings.Split(polarisapi.PolarisGrpc, ":")[1],
		"PolarisConfigIP":     strings.Split(polarisapi.PolarisConfigGrpc, ":")[0],
		"PolarisConfigPort":   strings.Split(polarisapi.PolarisConfigGrpc, ":")[1],
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
		// 查看用户是否自定义了相关配置信息
		// 需要根据用户的自定义参数信息，将 agent 的特定 application.properties 文件注入到 javaagent-init 中
		if properties, ok := annonations[customJavaAgentPluginConfig]; ok {
			customProperties := map[string]string{}
			if err := json.Unmarshal([]byte(properties), &customProperties); err != nil {
				return err
			}
			// 先从 configmap 中获取 java-agent 不同 plugin-type 的默认配置信息
			for k, v := range customProperties {
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

func nameOfPluginDefault(v string) string {
	return v + "-default-properties"
}

func (pb *PodPatchBuilder) updateContainer(opt *inject.PatchOptions, sidecarMode utils.SidecarMode, pod *corev1.Pod,
	target []corev1.Container, basePath string) []inject.Rfc6902PatchOperation {

	patchs := make([]inject.Rfc6902PatchOperation, 0, len(target))

	annonations := pod.Annotations
	if val, ok := annonations[customJavaAgentVersion]; ok && val != "" {
		opt.ExternalInfo[customJavaAgentVersion] = val
	} else {
		annonations[customJavaAgentVersion] = "latest"
	}

	defaultProperties := make(map[string]string)
	var javaToolOptionsValue string

	for index, container := range target {
		envs := container.Env
		javaEnvIndex := -1
		if _, valid := oldAgentVersions[annonations[customJavaAgentVersion]]; !valid {
			if properties, ok := annonations[customJavaAgentPluginConfig]; ok {
				customProperties := map[string]string{}
				if properties != "" {
					json.Unmarshal([]byte(properties), &customProperties)
				}
				// 先从 configmap 中获取 java-agent 不同 plugin-type 的默认配置信息
				for k, v := range customProperties {
					defaultProperties[k] = v
				}
			}
		}

		for key, value := range defaultProperties {
			javaToolOptionsValue += fmt.Sprintf(" -D%s=%s", key, value)
		}

		if len(envs) != 0 {
			for i := range envs {
				if envs[i].Name == "JAVA_TOOL_OPTIONS" {
					javaEnvIndex = i
					break
				}
			}
			if javaEnvIndex != -1 {
				oldVal := envs[javaEnvIndex].Value
				if _, valid := oldAgentVersions[annonations[customJavaAgentVersion]]; !valid {
					envs[javaEnvIndex] = corev1.EnvVar{
						Name:  "JAVA_TOOL_OPTIONS",
						Value: oldVal + " " + ActiveJavaAgentCmd + javaToolOptionsValue,
					}
				} else {
					envs[javaEnvIndex] = corev1.EnvVar{
						Name:  "JAVA_TOOL_OPTIONS",
						Value: oldVal + " " + fmt.Sprintf(OldActiveJavaAgentCmd, opt.ExternalInfo[customJavaAgentVersion]),
					}
				}
			}
		}
		if javaEnvIndex == -1 {
			// 注入 java agent 需要用到的参数信息
			if _, valid := oldAgentVersions[annonations[customJavaAgentVersion]]; !valid {
				container.Env = append(container.Env, corev1.EnvVar{
					Name:  "JAVA_TOOL_OPTIONS",
					Value: ActiveJavaAgentCmd + javaToolOptionsValue,
				})
			} else {
				container.Env = append(container.Env, corev1.EnvVar{
					Name:  "JAVA_TOOL_OPTIONS",
					Value: fmt.Sprintf(OldActiveJavaAgentCmd, opt.ExternalInfo[customJavaAgentVersion]),
				})
			}
		}

		// container 需要新挂载磁盘
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				Name:      "java-agent-dir",
				MountPath: "/app/lib/.polaris/java_agent",
			})

		path := basePath
		path += "/" + strconv.Itoa(index)
		patchs = append(patchs, inject.Rfc6902PatchOperation{
			Op:    "replace",
			Path:  path,
			Value: container,
		})
	}
	return patchs
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
