// ==== 原代码 ====
// ... 省略其他代码 ...

// ==== 修改后代码 ====
package javaagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/base"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func TestUpdateContainer(t *testing.T) {
	// 定义测试用例
	tests := []struct {
		name        string
		pod         *corev1.Pod
		opt         *inject.PatchOptions
		expectedCmd string
		expectMount bool
	}{
		{
			name: "旧版本使用的javaagent参数需要带版本号",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Annotations: map[string]string{
						util.AnnotationKeyJavaAgentVersion: "1.7.0-RC1",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "main",
						Env: []corev1.EnvVar{{
							Name:  "JAVA_TOOL_OPTIONS",
							Value: "-Xmx512m",
						}},
					}},
				},
			},
			opt: &inject.PatchOptions{
				WorkloadName: "test-workload",
				ExternalInfo: map[string]string{},
				Pod:          &corev1.Pod{},
			},
			expectedCmd: "-Xmx512m -javaagent:/app/lib/.polaris/java_agent/polaris-java-agent-1.7.0-RC1/polaris-agent-core-bootstrap.jar",
			expectMount: true,
		},
		{
			name: "使用k8s工作负载和命名空间作为服务空间和服务名",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Annotations: map[string]string{
						util.AnnotationKeyInjectJavaAgent:                     util.InjectionValueTrue,
						util.AnnotationKeyWorkloadNameAsServiceName:           util.InjectionValueTrue,
						util.AnnotationKeyWorkloadNamespaceAsServiceNamespace: util.InjectionValueTrue,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "main",
						Env:  []corev1.EnvVar{},
					}},
				},
			},
			opt: &inject.PatchOptions{
				ExternalInfo: make(map[string]string),
				WorkloadName: "test-workload",
				Annotations:  map[string]string{},
				Pod:          &corev1.Pod{},
			},
			expectedCmd: "-javaagent:/app/lib/.polaris/java_agent/polaris-java-agent/polaris-agent-core-bootstrap.jar -Dspring.cloud.polaris.discovery.namespace=test-ns -Dspring.application.name=test-workload",
			expectMount: true,
		},
		{
			name: "自定义服务名和使用k8s工作负载作为服务名有冲突,使用自定义的服务名",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Annotations: map[string]string{
						util.AnnotationKeyJavaAgentVersion:                    "v1.10.0",
						util.AnnotationKeyWorkloadNameAsServiceName:           util.InjectionValueTrue,
						util.AnnotationKeyWorkloadNamespaceAsServiceNamespace: util.InjectionValueTrue,
						util.AnnotationKeyJavaAgentPluginConfig:               `{"spring.application.name":"custom-service","spring.cloud.polaris.discovery.namespace":"custom-ns"}`,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "main",
						Env:  []corev1.EnvVar{},
					}},
				},
			},
			opt: &inject.PatchOptions{
				ExternalInfo: make(map[string]string),
				WorkloadName: "test-workload",
				Annotations:  map[string]string{},
				Pod:          &corev1.Pod{},
			},
			expectedCmd: "-javaagent:/app/lib/.polaris/java_agent/polaris-java-agent/polaris-agent-core-bootstrap.jar -Dspring.cloud.polaris.discovery.namespace=custom-ns -Dspring.application.name=custom-service",
			expectMount: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 初始化测试环境
			pb := &PodPatchBuilder{
				PodPatchBuilder: &base.PodPatchBuilder{},
			}

			// 构建请求
			req := &inject.OperateContainerRequest{
				Option:   tt.opt,
				BasePath: "/spec/containers",
			}
			tt.opt.Pod = tt.pod

			// 执行测试
			patches := pb.updateContainer(req)

			// 验证结果
			assert.NotEmpty(t, patches, "should generate patches")
			if len(patches) > 0 {
				container := patches[0].Value.(corev1.Container)

				// 验证环境变量
				var javaToolOptions string
				for _, env := range container.Env {
					if env.Name == "JAVA_TOOL_OPTIONS" {
						javaToolOptions = env.Value
						break
					}
				}
				assert.Contains(t, javaToolOptions, tt.expectedCmd, "JAVA_TOOL_OPTIONS mismatch")

				// 验证Volume Mount
				if tt.expectMount {
					assert.Len(t, container.VolumeMounts, 1, "should have volume mount")
					assert.Equal(t, "java-agent-dir", container.VolumeMounts[0].Name)
				}

				// 验证补丁操作
				assert.Equal(t, "replace", patches[0].Op, "patch operation should be replace")
			}

			// 验证版本处理
			if version, ok := tt.pod.Annotations[util.AnnotationKeyJavaAgentVersion]; ok {
				if _, isOld := oldAgentVersions[version]; isOld {
					assert.Contains(t, tt.opt.ExternalInfo, util.AnnotationKeyJavaAgentVersion,
						"should store version in external info for old agents")
				}
			}
		})
	}
}

func TestHandleJavaAgentInit(t *testing.T) {
	// Mock 测试用的 ConfigMap
	mockConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plugin-default.properties",
			Namespace: util.RootNamespace,
		},
		Data: map[string]string{
			"spring-cloud-default-properties": `spring.application.name={{.MicroserviceName}}
spring.cloud.polaris.discovery.namespace={{.MicroserviceNamespace}}`,
		},
	}

	tests := []struct {
		name           string
		podAnnotations map[string]string
		initContainer  *corev1.Container
		expected       func(t *testing.T, c *corev1.Container)
	}{
		{
			name:           "default_image_version",
			podAnnotations: map[string]string{},
			initContainer: &corev1.Container{
				Image: "polarismesh/polaris-java-agent-init:1.7.0-RC5",
			},
			expected: func(t *testing.T, c *corev1.Container) {
				assert.Equal(t, "polarismesh/polaris-java-agent-init:1.7.0-RC5", c.Image)
				assertContainsEnv(t, c, "POLARIS_SERVER_IP")
				assertContainsEnv(t, c, "POLARIS_DISCOVER_PORT")
			},
		},
		{
			name: "custom_image_version",
			podAnnotations: map[string]string{
				util.AnnotationKeyJavaAgentVersion: "v1.8.0",
			},
			initContainer: &corev1.Container{
				Image: "polarismesh/polaris-java-agent-init:1.7.0-RC5",
			},
			expected: func(t *testing.T, c *corev1.Container) {
				assert.Equal(t, "polarismesh/polaris-java-agent-init:v1.8.0", c.Image)
			},
		},
		{
			name: "old_version_with_configmap",
			podAnnotations: map[string]string{
				util.AnnotationKeyJavaAgentVersion:         "1.7.0-RC5",
				util.AnnotationKeyJavaAgentPluginConfig:    `{"logging.level":"DEBUG"}`,
				util.AnnotationKeyJavaAgentPluginFramework: "spring-cloud",
			},
			initContainer: &corev1.Container{
				Image: "polarismesh/polaris-java-agent-init:1.7.0-RC5",
			},
			expected: func(t *testing.T, c *corev1.Container) {
				// 验证环境变量
				var pluginConf string
				for _, env := range c.Env {
					if env.Name == "JAVA_AGENT_PLUGIN_CONF" {
						pluginConf = env.Value
					}
				}
				assert.Contains(t, pluginConf, "spring.application.name=test-service")
				assert.Contains(t, pluginConf, "logging.level=DEBUG")
				assert.Contains(t, pluginConf, "spring.cloud.polaris.discovery.namespace=default")
			},
		},
		{
			name: "使用k8s命名空间和工作负载名称注册",
			podAnnotations: map[string]string{
				util.AnnotationKeyJavaAgentVersion:                    "1.7.0-RC5",
				util.AnnotationKeyJavaAgentPluginConfig:               `{"logging.level":"DEBUG"}`,
				util.AnnotationKeyJavaAgentPluginFramework:            "spring-cloud",
				util.AnnotationKeyWorkloadNameAsServiceName:           util.InjectionValueTrue,
				util.AnnotationKeyWorkloadNamespaceAsServiceNamespace: util.InjectionValueTrue,
			},
			initContainer: &corev1.Container{
				Image: "polarismesh/polaris-java-agent-init:1.7.0-RC5",
			},
			expected: func(t *testing.T, c *corev1.Container) {
				// 验证环境变量
				var pluginConf string
				for _, env := range c.Env {
					if env.Name == "JAVA_AGENT_PLUGIN_CONF" {
						pluginConf = env.Value
					}
				}
				assert.Contains(t, pluginConf, "spring.application.name=test-service")
				assert.Contains(t, pluginConf, "logging.level=DEBUG")
				assert.Contains(t, pluginConf, "spring.cloud.polaris.discovery.namespace=test-ns")
			},
		},
		{
			name: "使用k8s命名空间和工作负载名称注册,和自定义配置冲突,使用自定义配置",
			podAnnotations: map[string]string{
				util.AnnotationKeyJavaAgentVersion:                    "1.7.0-RC5",
				util.AnnotationKeyJavaAgentPluginConfig:               `{"logging.level":"DEBUG","spring.cloud.polaris.discovery.namespace":"custom-ns","spring.application.name":"custom-service"}`,
				util.AnnotationKeyJavaAgentPluginFramework:            "spring-cloud",
				util.AnnotationKeyWorkloadNameAsServiceName:           util.InjectionValueTrue,
				util.AnnotationKeyWorkloadNamespaceAsServiceNamespace: util.InjectionValueTrue,
			},
			initContainer: &corev1.Container{
				Image: "polarismesh/polaris-java-agent-init:1.7.0-RC5",
			},
			expected: func(t *testing.T, c *corev1.Container) {
				// 验证环境变量
				var pluginConf string
				for _, env := range c.Env {
					if env.Name == "JAVA_AGENT_PLUGIN_CONF" {
						pluginConf = env.Value
					}
				}
				assert.Contains(t, pluginConf, "spring.application.name=custom-service")
				assert.Contains(t, pluginConf, "logging.level=DEBUG")
				assert.Contains(t, pluginConf, "spring.cloud.polaris.discovery.namespace=custom-ns")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 初始化fake client
			client := fake.NewSimpleClientset(mockConfigMap)

			// 构建测试请求
			req := &inject.OperateContainerRequest{
				Option: &inject.PatchOptions{
					KubeClient: client,
					Pod: &corev1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Namespace:   "test-ns",
							Annotations: tt.podAnnotations,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  "main",
								Image: "app:latest",
							}},
						},
					},
					ExternalInfo: make(map[string]string),
					WorkloadName: "test-service",
					Annotations:  map[string]string{util.SidecarServiceName: "test-service"},
				},
				External: []corev1.Container{*tt.initContainer},
			}

			pb := &PodPatchBuilder{
				PodPatchBuilder: &base.PodPatchBuilder{},
			}

			// 执行测试
			err := pb.handleJavaAgentInit(req, tt.initContainer)

			// 验证结果
			assert.NoError(t, err)
			tt.expected(t, tt.initContainer)

			// 验证外部信息存储
			if version, ok := tt.podAnnotations[util.AnnotationKeyJavaAgentVersion]; ok {
				assert.Equal(t, version, req.Option.ExternalInfo[util.AnnotationKeyJavaAgentVersion])
			}
		})
	}
}

// 辅助断言函数
func assertContainsEnv(t *testing.T, container *corev1.Container, name string) {
	for _, env := range container.Env {
		if env.Name == name {
			return
		}
	}
	t.Errorf("Expected env %s not found", name)
}
