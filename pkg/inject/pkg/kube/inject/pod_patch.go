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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

var (
	// 定义一个变量，用于记录当前正在使用的 patch builder
	_PatchBuilders = map[string]PodPatchBuilder{}
)

func RegisterPatchBuilder(name string, pb PodPatchBuilder) {
	_PatchBuilders[name] = pb
}

type PatchType int32

const (
	_ PatchType = iota
	// PatchType_Remove 删除操作
	PatchType_Remove
	// PatchType_Add 增加操作
	PatchType_Add
	// PatchType_Update 更新操作
	PatchType_Update
)

type PatchOptions struct {
	KubeClient kubernetes.Interface
	// Sidecar 的运行模式
	SidecarMode utils.SidecarMode
	// 目标操作的 POD
	Pod *corev1.Pod
	// 如果 POD 之前已经注入过 Sidecar，那么这里会记录之前的状态信息
	PrevStatus *SidecarInjectionStatus
	// 需要增加的注解信息
	Annotations map[string]string
	// 准备注入 POD 的 Sidecar 的新信息
	Sic *SidecarInjectionSpec
	// Workload 的名称
	WorkloadName string
	// ExternalInfo .
	ExternalInfo map[string]string
}

type OperateContainerRequest struct {
	// 操作类型
	Type     PatchType
	Option   *PatchOptions
	Source   []corev1.Container
	External []corev1.Container
	BasePath string
}

type OperateVolumesRequest struct {
	// 操作类型
	Type     PatchType
	Option   *PatchOptions
	Source   []corev1.Volume
	External []corev1.Volume
	BasePath string
}

type OperateImagePullSecretsRequest struct {
	// 操作类型
	Type     PatchType
	Option   *PatchOptions
	Source   []corev1.LocalObjectReference
	External []corev1.LocalObjectReference
	BasePath string
}

// PodPatchBuilder
type PodPatchBuilder interface {
	PatchContainer(*OperateContainerRequest) ([]Rfc6902PatchOperation, error)
	PatchVolumes(*OperateVolumesRequest) ([]Rfc6902PatchOperation, error)
	PatchImagePullSecrets(*OperateImagePullSecretsRequest) ([]Rfc6902PatchOperation, error)
	PatchSecurityContext() ([]Rfc6902PatchOperation, error)
	PatchDnsConfig() ([]Rfc6902PatchOperation, error)
}
