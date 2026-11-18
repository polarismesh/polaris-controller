// Tencent is pleased to support the open source community by making Polaris available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package deploy_test

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"

	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

type webhookManifest struct {
	Kind     string `json:"kind"`
	Webhooks []struct {
		Name         string `json:"name"`
		ClientConfig struct {
			CABundle string `json:"caBundle"`
		} `json:"clientConfig"`
	} `json:"webhooks"`
}

func TestRawWebhooksUseControllerCA(t *testing.T) {
	for _, version := range []string{"v1.21", "v1.22"} {
		t.Run(version, func(t *testing.T) {
			dir := filepath.Join("kubernetes_"+version, "kubernetes")
			ca, err := os.ReadFile(filepath.Join(dir, "secrets", "ca-cert.pem"))
			if err != nil {
				t.Fatal(err)
			}
			expected := base64.StdEncoding.EncodeToString(ca)

			file, err := os.Open(filepath.Join(dir, "injector.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			decoder := yamlutil.NewYAMLOrJSONDecoder(file, 4096)
			webhookCount := 0
			for {
				var manifest webhookManifest
				err := decoder.Decode(&manifest)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if manifest.Kind != "MutatingWebhookConfiguration" {
					continue
				}
				for _, webhook := range manifest.Webhooks {
					webhookCount++
					if webhook.ClientConfig.CABundle != expected {
						t.Errorf("%s caBundle does not match secrets/ca-cert.pem", webhook.Name)
					}
				}
			}
			if webhookCount != 3 {
				t.Fatalf("got %d webhooks, want 3", webhookCount)
			}
		})
	}
}
