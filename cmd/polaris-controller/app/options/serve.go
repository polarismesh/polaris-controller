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
	"context"
	"fmt"
	"github.com/polarismesh/polaris-controller/pkg/util/configz"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/apiserver/pkg/server/routes"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/component-base/metrics/legacyregistry"
	_ "k8s.io/component-base/metrics/prometheus/workqueue" // for workqueue metric registration
	"k8s.io/klog"
	"net/http"
	goruntime "runtime"
	"time"
)

// BuildHandlerChain builds a handler chain with a base handler and CompletedConfig.
func BuildHandlerChain(apiHandler http.Handler) http.Handler {
	requestInfoResolver := &apirequest.RequestInfoFactory{}
	handler := apiHandler

	handler = genericapifilters.WithRequestInfo(handler, requestInfoResolver)
	handler = genericapifilters.WithCacheControl(handler)
	handler = genericfilters.WithPanicRecovery(handler)

	return handler
}

// NewBaseHandler takes in CompletedConfig and returns a handler.
func NewBaseHandler(c *componentbaseconfig.DebuggingConfiguration,
	checks ...healthz.HealthChecker) *mux.PathRecorderMux {
	mux := mux.NewPathRecorderMux("controller-manager")
	healthz.InstallHandler(mux, checks...)
	if c.EnableProfiling {
		routes.Profiling{}.Install(mux)
		if c.EnableContentionProfiling {
			goruntime.SetBlockProfileRate(1)
		}
	}
	configz.InstallHandler(mux)
	////lint:ignore SA1019 See the Metrics Stability Migration KEP
	mux.Handle("/metrics", legacyregistry.Handler())

	return mux
}

// Serve starts an insecure http server with the given handler. It fails only if
// the initial listen call fails. It does not block.
func RunServe(handler http.Handler, bindPort int32, shutdownTimeout time.Duration, stopCh <-chan struct{}) error {
	server := &http.Server{
		Addr:           fmt.Sprintf(":%v", bindPort),
		Handler:        handler,
		MaxHeaderBytes: 1 << 20,
	}

	_, err := RunServer(server, shutdownTimeout, stopCh)
	// NOTE: we do not handle stoppedCh returned by RunServer for graceful termination here
	return err
}

// RunServer
func RunServer(
	server *http.Server,
	shutDownTimeout time.Duration,
	stopCh <-chan struct{},
) (<-chan struct{}, error) {

	// Shutdown server gracefully.
	stoppedCh := make(chan struct{})
	go func() {
		defer close(stoppedCh)
		<-stopCh
		ctx, cancel := context.WithTimeout(context.Background(), shutDownTimeout)
		server.Shutdown(ctx)
		cancel()
	}()

	go func() {
		defer utilruntime.HandleCrash()

		err := server.ListenAndServe()

		select {
		case <-stopCh:
			klog.Infof("Stop Listening %s", server.Addr)
		default:
			panic(fmt.Sprintf("%s due to error: %v", server.Addr, err))
		}
	}()

	return stoppedCh, nil
}
