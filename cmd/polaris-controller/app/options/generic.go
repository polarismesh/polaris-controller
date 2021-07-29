/*
Copyright 2018 The Kubernetes Authors.

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

package options

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cliflag "k8s.io/component-base/cli/flag"
	componentbaseconfig "k8s.io/component-base/config"
)

const (
	// DefaultKubeApiQps kube-api-qps is 200.
	DefaultKubeApiQps = 200
	// DefaultKubeApiBurst kube-api-burst is 500.
	DefaultKubeApiBurst = 500
	// DefaultKubeApiBurst kube-api-burst is 500.
	DefaultKubePort = 80
)

// GenericControllerManagerConfiguration holds configuration for a generic controller-manager
type GenericControllerManagerConfiguration struct {
	// port is the port that the controller-manager's http service runs on.
	Port int32
	// address is the IP address to serve on (set to 0.0.0.0 for all interfaces).
	Address string
	// minResyncPeriod is the resync period in reflectors; will be random between
	// minResyncPeriod and 2*minResyncPeriod.
	MinResyncPeriod metav1.Duration
	// ClientConnection specifies the kubeconfig file and client connection
	// settings for the proxy server to use when communicating with the apiserver.
	ClientConnection componentbaseconfig.ClientConnectionConfiguration
	// How long to wait between starting controller managers
	ControllerStartInterval metav1.Duration
	// leaderElection defines the configuration of leader election client.
	LeaderElection componentbaseconfig.LeaderElectionConfiguration

	// DebuggingConfiguration holds configuration for Debugging related features.
	Debugging componentbaseconfig.DebuggingConfiguration
}

// GenericControllerManagerConfigurationOptions holds the options which are generic.
type GenericControllerManagerConfigurationOptions struct {
	*GenericControllerManagerConfiguration
	Debugging *DebuggingOptions
}

// NewGenericControllerManagerConfigurationOptions returns generic configuration default values for both
// the kube-controller-manager and the cloud-controller-manager. Any common changes should
// be made here. Any individual changes should be made in that controller.
func NewGenericControllerManagerConfigurationOptions(
	cfg *GenericControllerManagerConfiguration) *GenericControllerManagerConfigurationOptions {

	o := &GenericControllerManagerConfigurationOptions{
		GenericControllerManagerConfiguration: cfg,
		Debugging: &DebuggingOptions{
			DebuggingConfiguration: &componentbaseconfig.DebuggingConfiguration{},
		},
	}
	return o
}

// AddFlags adds flags related to generic for controller manager to the specified FlagSet.
func (o *GenericControllerManagerConfigurationOptions) AddFlags(fss *cliflag.NamedFlagSets) {
	if o == nil {
		return
	}

	o.Debugging.AddFlags(fss.FlagSet("debugging"))
	genericfs := fss.FlagSet("generic")
	genericfs.DurationVar(
		&o.MinResyncPeriod.Duration, "min-resync-period",
		o.MinResyncPeriod.Duration,
		"The resync period in reflectors will be random between MinResyncPeriod and 2*MinResyncPeriod.")
	genericfs.StringVar(
		&o.ClientConnection.ContentType, "kube-api-content-type",
		o.ClientConnection.ContentType, "Content type of requests sent to apiserver.")
	genericfs.Float32Var(
		&o.ClientConnection.QPS, "kube-api-qps", DefaultKubeApiQps,
		"QPS to use while talking with kubernetes apiserver.")
	genericfs.Int32Var(
		&o.ClientConnection.Burst, "kube-api-burst", DefaultKubeApiBurst,
		"Burst to use while talking with kubernetes apiserver.")
	genericfs.Int32Var(
		&o.Port, "kube-port", DefaultKubePort, "Port to use while talking with metrics and debugger.")

	BindFlags(&o.LeaderElection, genericfs)
}

// ApplyTo fills up generic config with options.
func (o *GenericControllerManagerConfigurationOptions) ApplyTo(cfg *GenericControllerManagerConfiguration) error {
	if o == nil {
		return nil
	}

	if err := o.Debugging.ApplyTo(&cfg.Debugging); err != nil {
		return err
	}

	cfg.Port = o.Port
	cfg.Address = o.Address
	cfg.MinResyncPeriod = o.MinResyncPeriod
	cfg.ClientConnection = o.ClientConnection
	cfg.ControllerStartInterval = o.ControllerStartInterval
	cfg.LeaderElection = o.LeaderElection

	return nil
}
