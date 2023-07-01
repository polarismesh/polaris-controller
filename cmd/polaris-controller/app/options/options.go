/*
Copyright 2014 The Kubernetes Authors.

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

// Package options provides the flags used for the controller manager.
package options

import (
	v1 "k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	clientgokubescheme "k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	cliflag "k8s.io/component-base/cli/flag"

	"github.com/polarismesh/polaris-controller/common/log"
	utilfeature "github.com/polarismesh/polaris-controller/pkg/util/feature"
)

const (
	// PolarisControllerManagerUserAgent is the userAgent name when starting kube-controller managers.
	PolarisControllerManagerUserAgent = "polaris-controller-manager"
	PolarisBindPort                   = 80
)

// KubeControllerManagerConfiguration contains elements describing kube-controller manager.
type KubeControllerManagerConfiguration struct {
	// Generic holds configuration for a generic controller-manager
	Generic           GenericControllerManagerConfiguration
	PolarisController PolarisControllerConfiguration
}

// Options contains everything necessary to create and run controller-manager.
type KubeControllerManagerOptions struct {
	Generic           *GenericControllerManagerConfigurationOptions
	PolarisController *PolarisControllerOptions
	CloudConfig       string
	BindPort          int32
	RateLimit         int
	KubeConfig        *restclient.Config
	Master            string
	Kubeconfig        string
}

// NewPolarisControllerOptions
func NewPolarisControllerOptions() *KubeControllerManagerOptions {
	componentConfig := KubeControllerManagerConfiguration{}
	s := KubeControllerManagerOptions{
		Generic: NewGenericControllerManagerConfigurationOptions(&componentConfig.Generic),
		PolarisController: &PolarisControllerOptions{
			&componentConfig.PolarisController,
		},
	}
	s.Generic.Port = s.BindPort
	return &s
}

// Flags returns flags for a specific APIServer by section name
func (s *KubeControllerManagerOptions) Flags() cliflag.NamedFlagSets {
	fss := cliflag.NamedFlagSets{}
	s.Generic.AddFlags(&fss)
	s.PolarisController.AddFlags(fss.FlagSet("polaris controller"))
	fs := fss.FlagSet("misc")
	fs.StringVar(&s.Master, "master", s.Master,
		"The address of the Kubernetes API server (overrides any value in kubeconfig).")
	fs.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig,
		"Path to kubeconfig file with authorization and master location information.")
	fs.Int32Var(&s.BindPort, "bind-port", PolarisBindPort, "Polaris manager port.")
	utilfeature.DefaultMutableFeatureGate.AddFlag(fss.FlagSet("generic"))

	return fss
}

// Config
func (s KubeControllerManagerOptions) Config() (*Config, error) {
	kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
	if err != nil {
		return nil, err
	}
	kubeconfig.DisableCompression = true
	kubeconfig.ContentConfig.AcceptContentTypes = s.Generic.ClientConnection.AcceptContentTypes
	kubeconfig.ContentConfig.ContentType = s.Generic.ClientConnection.ContentType
	kubeconfig.QPS = s.Generic.ClientConnection.QPS
	kubeconfig.Burst = int(s.Generic.ClientConnection.Burst)

	client, err := clientset.NewForConfig(restclient.AddUserAgent(kubeconfig, PolarisControllerManagerUserAgent))
	if err != nil {
		return nil, err
	}

	// shallow copy, do not modify the kubeconfig.Timeout.
	config := *kubeconfig
	config.Timeout = s.Generic.LeaderElection.RenewDeadline.Duration
	leaderElectionClient := clientset.NewForConfigOrDie(restclient.AddUserAgent(&config, "leader-election"))

	eventRecorder := createRecorder(client, PolarisControllerManagerUserAgent)

	c := &Config{
		Client:               client,
		Kubeconfig:           kubeconfig,
		EventRecorder:        eventRecorder,
		LeaderElectionClient: leaderElectionClient,
	}
	if err := s.ApplyTo(c); err != nil {
		return nil, err
	}

	return c, nil
}

// ApplyTo fills up controller manager config with options.
func (s *KubeControllerManagerOptions) ApplyTo(c *Config) error {
	if err := s.Generic.ApplyTo(&c.ComponentConfig.Generic); err != nil {
		return err
	}
	if err := s.PolarisController.ApplyTo(&c.ComponentConfig.PolarisController); err != nil {
		return err
	}

	return nil
}

// createRecorder
func createRecorder(kubeClient clientset.Interface, userAgent string) record.EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(log.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	return eventBroadcaster.NewRecorder(clientgokubescheme.Scheme, v1.EventSource{Component: userAgent})
}
