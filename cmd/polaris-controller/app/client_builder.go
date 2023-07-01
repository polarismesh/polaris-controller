/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package app

import (
	"github.com/polarismesh/polaris-controller/common/log"
	"go.uber.org/zap"
	clientset "k8s.io/client-go/kubernetes"
	v1authentication "k8s.io/client-go/kubernetes/typed/authentication/v1"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
)

// ControllerClientBuilder allows you to get clients and configs for controllers
// Please note a copy also exists in staging/src/k8s.io/cloud-provider/cloud.go
// TODO: Extract this into a separate controller utilities repo (issues/68947)
type ControllerClientBuilder interface {
	Config(name string) (*restclient.Config, error)
	ConfigOrDie(name string) *restclient.Config
	Client(name string) (clientset.Interface, error)
	ClientOrDie(name string) clientset.Interface
}

// SimpleControllerClientBuilder returns a fixed client with different user agents
type SimpleControllerClientBuilder struct {
	// ClientConfig is a skeleton config to clone and use as the basis for each controller client
	ClientConfig *restclient.Config
}

// Config
func (b SimpleControllerClientBuilder) Config(name string) (*restclient.Config, error) {
	clientConfig := *b.ClientConfig
	return restclient.AddUserAgent(&clientConfig, name), nil
}

// ConfigOrDie
func (b SimpleControllerClientBuilder) ConfigOrDie(name string) *restclient.Config {
	clientConfig, err := b.Config(name)
	if err != nil {
		log.Fatal("ConfigOrDie", zap.Error(err))
	}
	return clientConfig
}

// Client
func (b SimpleControllerClientBuilder) Client(name string) (clientset.Interface, error) {
	clientConfig, err := b.Config(name)
	if err != nil {
		return nil, err
	}
	return clientset.NewForConfig(clientConfig)
}

// ClientOrDie
func (b SimpleControllerClientBuilder) ClientOrDie(name string) clientset.Interface {
	client, err := b.Client(name)
	if err != nil {
		log.Fatal("ClientOrDie", zap.Error(err))
	}
	return client
}

// SAControllerClientBuilder is a ControllerClientBuilder that returns clients identifying as
// service accounts
type SAControllerClientBuilder struct {
	// ClientConfig is a skeleton config to clone and use as the basis for each controller client
	ClientConfig *restclient.Config

	// CoreClient is used to provision service accounts if needed and watch for their associated tokens
	// to construct a controller client
	CoreClient v1core.CoreV1Interface

	// AuthenticationClient is used to check API tokens to make sure they are valid before
	// building a controller client from them
	AuthenticationClient v1authentication.AuthenticationV1Interface

	// Namespace is the namespace used to host the service accounts that will back the
	// controllers.  It must be highly privileged namespace which normal users cannot inspect.
	Namespace string
}
