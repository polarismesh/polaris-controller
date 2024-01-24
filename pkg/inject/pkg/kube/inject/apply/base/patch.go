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

package base

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	utils "github.com/polarismesh/polaris-controller/pkg/util"
)

// PodPatchBuilder
type PodPatchBuilder struct {
}

func (pb *PodPatchBuilder) PatchContainer(req *inject.OperateContainerRequest) ([]inject.Rfc6902PatchOperation, error) {
	switch req.Type {
	case inject.PatchType_Add:
		patchs := pb.addContainers(req.Option.SidecarMode, req.Option.Pod, req.Source, req.External, req.BasePath)
		return patchs, nil
	case inject.PatchType_Remove:
		patchs := pb.removeContainers(req.Source, req.External, req.BasePath)
		return patchs, nil
	}
	return nil, nil
}

// JSONPatch `remove` is applied sequentially. Remove items in reverse
// order to avoid renumbering indices.
func (pb *PodPatchBuilder) removeContainers(containers, removed []corev1.Container, path string) []inject.Rfc6902PatchOperation {
	patch := make([]inject.Rfc6902PatchOperation, 0, 4)

	names := map[string]bool{}
	for _, container := range removed {
		names[container.Name] = true
	}
	for i := len(containers) - 1; i >= 0; i-- {
		if _, ok := names[containers[i].Name]; ok {
			patch = append(patch, inject.Rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func (pb *PodPatchBuilder) addContainers(sidecarMode utils.SidecarMode, pod *corev1.Pod, target, added []corev1.Container, basePath string) []inject.Rfc6902PatchOperation {

	patch := make([]inject.Rfc6902PatchOperation, 0, 4)

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
		if add.Name == inject.ProxyContainerName && saJwtSecretMountName != "" {
			// add service account secret volume mount(/var/run/secrets/kubernetes.io/serviceaccount,
			// https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/#service-account-automation) to istio-proxy container,
			// so that envoy could fetch/pass k8s sa jwt and pass to sds server, which will be used to request workload identity for the pod.
			add.VolumeMounts = append(add.VolumeMounts, saJwtSecretMount)
		}

		value = add
		path := basePath
		if first {
			first = false
			value = []corev1.Container{add}
		} else {
			path += "/-"
		}
		patch = append(patch, inject.Rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func (pb *PodPatchBuilder) PatchVolumes(req *inject.OperateVolumesRequest) ([]inject.Rfc6902PatchOperation, error) {
	switch req.Type {
	case inject.PatchType_Add:
		patchs := addVolume(req.Source, req.External, req.BasePath)
		return patchs, nil
	case inject.PatchType_Remove:
		patchs := removeVolumes(req.Source, req.External, req.BasePath)
		return patchs, nil
	}
	return nil, nil
}

func addVolume(target, added []corev1.Volume, basePath string) (patch []inject.Rfc6902PatchOperation) {
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
		patch = append(patch, inject.Rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func removeVolumes(volumes, removed []corev1.Volume, path string) (patch []inject.Rfc6902PatchOperation) {
	names := map[string]bool{}
	for _, volume := range removed {
		names[volume.Name] = true
	}
	for i := len(volumes) - 1; i >= 0; i-- {
		if _, ok := names[volumes[i].Name]; ok {
			patch = append(patch, inject.Rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func (pb *PodPatchBuilder) PatchImagePullSecrets(req *inject.OperateImagePullSecretsRequest) ([]inject.Rfc6902PatchOperation, error) {
	switch req.Type {
	case inject.PatchType_Add:
		patchs := addImagePullSecrets(req.Source, req.External, req.BasePath)
		return patchs, nil
	case inject.PatchType_Remove:
		patchs := removeImagePullSecrets(req.Source, req.External, req.BasePath)
		return patchs, nil
	}
	return nil, nil
}

func addImagePullSecrets(target, added []corev1.LocalObjectReference, basePath string) (patch []inject.Rfc6902PatchOperation) {
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
		patch = append(patch, inject.Rfc6902PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		})
	}
	return patch
}

func removeImagePullSecrets(imagePullSecrets, removed []corev1.LocalObjectReference, path string) (patch []inject.Rfc6902PatchOperation) {
	names := map[string]bool{}
	for _, ref := range removed {
		names[ref.Name] = true
	}
	for i := len(imagePullSecrets) - 1; i >= 0; i-- {
		if _, ok := names[imagePullSecrets[i].Name]; ok {
			patch = append(patch, inject.Rfc6902PatchOperation{
				Op:   "remove",
				Path: fmt.Sprintf("%v/%v", path, i),
			})
		}
	}
	return patch
}

func (pb *PodPatchBuilder) PatchSecurityContext() ([]inject.Rfc6902PatchOperation, error) {
	return nil, nil
}

func (pb *PodPatchBuilder) PatchDnsConfig() ([]inject.Rfc6902PatchOperation, error) {
	return nil, nil
}
