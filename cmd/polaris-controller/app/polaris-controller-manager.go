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

package app

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/grpclog"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/component-base/version/verflag"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	"github.com/polarismesh/polaris-controller/common"
	"github.com/polarismesh/polaris-controller/common/log"
	polarisController "github.com/polarismesh/polaris-controller/pkg/controller"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/config"
	"github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject"
	_ "github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/javaagent"
	_ "github.com/polarismesh/polaris-controller/pkg/inject/pkg/kube/inject/apply/mesh"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	utilflag "github.com/polarismesh/polaris-controller/pkg/util/flag"
	"github.com/polarismesh/polaris-controller/pkg/version"
)

const (
	ControllerStartJitter           = 1.0
	ControllerName                  = "polaris-controller"
	PolarisAccountingControllerName = "polaris-accounting-controller"
	DefaultLockObjectName           = "polaris-controller"
	DefaultLeaderElectionName       = "polaris-controller"

	MeshConfigFile      = "/etc/polaris-inject/inject/mesh-config"
	DnsConfigFile       = "/etc/polaris-inject/inject/dns-config"
	JavaAgentConfigFile = "/etc/polaris-inject/inject/java-agent-config"
	ValuesFile          = "/etc/polaris-inject/inject/values"
	BootstrapConfigFile = "/etc/polaris-inject/config/mesh"
	CertFile            = "/etc/polaris-inject/certs/cert.pem"
	KeyFile             = "/etc/polaris-inject/certs/key.pem"
)

var (
	flags = struct {
		injectPort     int
		httpPort       int
		grpcPort       int
		monitoringPort int

		polarisServerAddress string
	}{}
	podIP string
)

// ControllerContext defines the context object for controller
type ControllerContext struct {
	// ClientBuilder will provide a client for this controller to use
	ClientBuilder ControllerClientBuilder

	// InformerFactory gives access to informers for the controller.
	InformerFactory informers.SharedInformerFactory

	// GenericInformerFactory gives access to informers for typed resources
	// and dynamic resources.
	GenericInformerFactory InformerFactory

	// ComponentConfig provides access to init options for a given controller
	ComponentConfig options.KubeControllerManagerConfiguration

	// DeferredDiscoveryRESTMapper is a RESTMapper that will defer
	// initialization of the RESTMapper until the first mapping is
	// requested.
	RESTMapper *restmapper.DeferredDiscoveryRESTMapper

	// Stop is the stop channel
	Stop <-chan struct{}

	// InformersStarted is closed after all of the controllers have been initialized and are running.
	// After this point it is safe,
	// for an individual controller to start the shared informers. Before it is closed, they should not.
	InformersStarted chan struct{}

	// ResyncPeriod generates a duration each time it is invoked; this is so that
	// multiple controllers don't get into lock-step and all hammer the apiserver
	// with list requests simultaneously.
	ResyncPeriod func() time.Duration
}

// NewPolarisControllerManagerCommand
func NewPolarisControllerManagerCommand() *cobra.Command {
	s := options.NewPolarisControllerOptions()

	cmd := &cobra.Command{
		Use:  "polaris-controller-manager",
		Long: `The polaris controller watch polaris service and update the instance of polaris service.`,
		Run: func(cmd *cobra.Command, args []string) {
			verflag.PrintAndExitIfRequested()
			utilflag.PrintFlags(cmd.Flags())
			initControllerConfig(s)
			c, err := s.Config()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}

			if err := Run(c.Complete(), wait.NeverStop); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}

	assignFlags(cmd)

	fs := cmd.Flags()
	namedFlagSets := s.Flags()
	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), cmd.Name())
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}
	// usageFmt := "Usage:\n  %s\n"
	// cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	// cmd.SetUsageFunc(func(cmd *cobra.Command) error {
	// 	fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine())
	// 	cliflag.PrintSections(cmd.OutOrStderr(), namedFlagSets, cols)
	// 	return nil
	// })
	// cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
	// 	fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
	// 	cliflag.PrintSections(cmd.OutOrStdout(), namedFlagSets, cols)
	// })
	return cmd
}

func initControllerConfig(s *options.KubeControllerManagerOptions) {
	// 读取配置文件
	config, err := readConfFromFile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read polaris server from config error %v \n", err)
		os.Exit(1)
	}

	// 初始化日志
	if err := log.Configure(config.Logger); err != nil {
		fmt.Fprintf(os.Stderr, "init logger from config error %v \n", err)
		os.Exit(1)
	}

	// 1. 配置 polaris server 地址
	var polarisServerAddress string
	// 优先使用启动参数指定的 polaris server 地址
	if flags.polarisServerAddress != "" {
		polarisServerAddress = flags.polarisServerAddress
	} else {
		polarisServerAddress = config.getPolarisServerAddress()
	}
	// 去除前后的空格字符
	polarisServerAddress = strings.TrimSpace(polarisServerAddress)
	polarisapi.PolarisHttpURL = "http://" + polarisServerAddress + ":" + strconv.Itoa(flags.httpPort)
	polarisapi.PolarisGrpc = polarisServerAddress + ":" + strconv.Itoa(flags.grpcPort)
	polarisapi.PolarisConfigGrpc = polarisServerAddress + ":8093"
	log.Infof("[Manager] polaris http address %s, discover grpc address %s, config grpc address %s",
		polarisapi.PolarisHttpURL, polarisapi.PolarisGrpc, polarisapi.PolarisConfigGrpc)
	polarisapi.PolarisAccessToken = config.getPolarisAccessToken()
	polarisapi.PolarisOperator = config.getPolarisOperator()

	// 2. 配置 polaris 同步模式
	if s.PolarisController.SyncMode == "" {
		// 优先用启动参数
		s.PolarisController.SyncMode = config.ServiceSync.Mode
	}

	// 3. 配置 clusterName
	if s.PolarisController.ClusterName == "" {
		// 优先用启动参数
		s.PolarisController.ClusterName = config.ClusterName
	}

	// 4. 配置 polaris-sidecar 注入模式的
	s.PolarisController.SidecarMode = config.SidecarInject.Mode
	// 设置是否开启同步 ConfigMap
	s.PolarisController.ConfigSync = config.ConfigSync

	// 5.设置健康检查时间以及定时对账时间
	s.PolarisController.HealthCheckDuration, _ = time.ParseDuration(config.Server.HealthCheckDuration)
	s.PolarisController.ResyncDuration, _ = time.ParseDuration(config.Server.ResyncDuration)

	common.PolarisServerAddress = polarisServerAddress
	common.PolarisServerGrpcAddress = polarisapi.PolarisGrpc

	log.Infof("load polaris server address: %s, polaris sync mode %s, polaris controller cluster name %s. \n",
		polarisServerAddress, s.PolarisController.SyncMode, s.PolarisController.ClusterName)
}

func assignFlags(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().IntVar(&flags.injectPort, "port", 9443, "Webhook port")
	rootCmd.PersistentFlags().IntVar(&flags.httpPort, "httpPort", 8090, "Http port")
	rootCmd.PersistentFlags().IntVar(&flags.grpcPort, "grpcPort", 8091, "Grpc port")
	rootCmd.PersistentFlags().StringVar(&(flags.polarisServerAddress), "polarisServerAddress", "",
		"polaris api address")
	rootCmd.PersistentFlags().IntVar(&flags.monitoringPort, "monitoringPort", 15014, "Webhook monitoring port")
}

func closeGrpcLog() {
	var (
		infoW    = io.Discard
		warningW = io.Discard
		errorW   = os.Stderr
	)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(infoW, warningW, errorW))
}

func initPolarisSidecarInjector(c *options.CompletedConfig) error {
	templateFilePath := config.TemplateFileConfig{
		MeshConfigFile:      MeshConfigFile,
		DnsConfigFile:       DnsConfigFile,
		JavaAgentConfigFile: JavaAgentConfigFile,
		ValuesFile:          ValuesFile,
		BootstrapConfigFile: BootstrapConfigFile,
		CertFile:            CertFile,
		KeyFile:             KeyFile,
	}
	parameters := inject.WebhookParameters{
		DefaultSidecarMode:  util.ParseSidecarMode(c.ComponentConfig.PolarisController.SidecarMode),
		TemplateFileConfig:  templateFilePath,
		Port:                flags.injectPort,
		HealthCheckInterval: 3 * time.Second,
		HealthCheckFile:     "/tmp/health",
		Client: SimpleControllerClientBuilder{
			ClientConfig: c.Kubeconfig,
		}.ClientOrDie("polaris-injector"),
	}

	wh, err := inject.NewWebhook(parameters)
	if err != nil {
		fmt.Printf("failed to create injection webhook, %s \n", err)
		return err
	}

	stop := make(chan struct{})

	go wh.Run(stop)

	return nil
}

// Run runs the KubeControllerManagerOptions.  This should never exit.
func Run(c *options.CompletedConfig, stopCh <-chan struct{}) error {
	// init sidecar injector
	if err := initPolarisSidecarInjector(c); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	closeGrpcLog()

	// To help debugging, immediately log version
	log.Infof("Version: %+v", version.Get())

	// Setup any healthz checks we will want to use.
	var checks []healthz.HealthChecker
	var electionChecker *leaderelection.HealthzAdaptor
	if c.ComponentConfig.Generic.LeaderElection.LeaderElect {
		electionChecker = leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)
		checks = append(checks, electionChecker)
	}

	unsecuredMux := options.NewBaseHandler(&c.ComponentConfig.Generic.Debugging, checks...)
	// handler := options.BuildHandlerChain(unsecuredMux)

	if err := options.RunServe(unsecuredMux, c.ComponentConfig.Generic.Port, 0, stopCh); err != nil {
		return err
	}

	run := func(ctx context.Context) {
		rootClientBuilder := SimpleControllerClientBuilder{
			ClientConfig: c.Kubeconfig,
		}
		controllerContext, err := CreateControllerContext(c, rootClientBuilder, rootClientBuilder, ctx.Done())
		if err != nil {
			log.Fatalf("error building controller context: %v", err)
		}

		if err := StartControllers(controllerContext, NewControllerInitializers(), unsecuredMux); err != nil {
			log.Fatalf("error starting controllers: %v", err)
		}

		controllerContext.InformerFactory.Start(controllerContext.Stop)
		controllerContext.GenericInformerFactory.Start(controllerContext.Stop)
		close(controllerContext.InformersStarted)

		select {}
	}

	if !c.ComponentConfig.Generic.LeaderElection.LeaderElect {
		run(context.TODO())
		panic("unreachable")
	}

	// add a uniquifier so that two processes on the same host don't accidentally both become active
	id := hostname() + "_" + string(uuid.NewUUID())

	rl, err := resourcelock.New(
		resourcelock.EndpointsLeasesResourceLock,
		c.ComponentConfig.Generic.LeaderElection.ResourceNamespace,
		DefaultLockObjectName,
		c.LeaderElectionClient.CoreV1(),
		c.LeaderElectionClient.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: c.EventRecorder,
		})
	if err != nil {
		panic(err)
	}

	// Try and become the leader and start cloud controller manager loops
	leaderelection.RunOrDie(context.TODO(), leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: c.ComponentConfig.Generic.LeaderElection.LeaseDuration.Duration,
		RenewDeadline: c.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:   c.ComponentConfig.Generic.LeaderElection.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: StoppedLeading,
			OnNewLeader:      NewLeader,
		},
		WatchDog: electionChecker,
		Name:     DefaultLeaderElectionName,
	})

	return nil
}

// hostname
func hostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	return hostname
}

// StoppedLeading invoked when this node stops being the leader
func StoppedLeading() {
	log.Infof("[INFO] %s: stopped leading", hostname())
}

// NewLeader invoked when a new leader is elected
func NewLeader(id string) {
	log.Infof("[INFO] %s: new leader: %s", hostname(), id)
}

// InitFunc is used to launch a particular controller.  It may run additional "should I activate checks".
// Any error returned will cause the controller process to `Fatal`
// The bool indicates whether the controller was enabled.
type InitFunc func(ctx ControllerContext) (debuggingHandler http.Handler, err error)

// CreateControllerContext creates a context struct containing references to resources needed by the
// controllers such as the cloud provider and clientBuilder. rootClientBuilder is only used for
// the shared-informers client and token controller.
func CreateControllerContext(s *options.CompletedConfig,
	rootClientBuilder, clientBuilder ControllerClientBuilder, stop <-chan struct{},
) (ControllerContext, error) {
	versionedClient := rootClientBuilder.ClientOrDie("shared-informers")
	sharedInformers := informers.NewSharedInformerFactory(versionedClient, ResyncPeriod(s)())

	metadataClient := metadata.NewForConfigOrDie(rootClientBuilder.ConfigOrDie("metadata-informers"))
	metadataInformers := metadatainformer.NewSharedInformerFactory(metadataClient, ResyncPeriod(s)())
	// If apiserver is not running we should wait for some time and fail only then. This is particularly
	// important when we start apiserver and controller manager at the same time.
	if err := util.WaitForAPIServer(versionedClient, 10*time.Second); err != nil {
		return ControllerContext{}, fmt.Errorf("failed to wait for apiserver being healthy: %v", err)
	}

	// Use a discovery client capable of being refreshed.
	discoveryClient := rootClientBuilder.ClientOrDie("controller-discovery")
	cachedClient := cacheddiscovery.NewMemCacheClient(discoveryClient.Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	go wait.Until(func() {
		restMapper.Reset()
	}, 30*time.Second, stop)

	ctx := ControllerContext{
		ClientBuilder:          clientBuilder,
		InformerFactory:        sharedInformers,
		GenericInformerFactory: NewInformerFactory(sharedInformers, metadataInformers),
		ComponentConfig:        s.ComponentConfig,
		RESTMapper:             restMapper,
		Stop:                   stop,
		InformersStarted:       make(chan struct{}),
		ResyncPeriod:           ResyncPeriod(s),
	}
	return ctx, nil
}

//// StartControllers starts a set of controllers with a specified ControllerContext
//func StartControllers(ctx ControllerContext, controller InitFunc, unsecuredMux *mux.PathRecorderMux) error {
//
//	debugHandler, err := controller(ctx)
//	if err != nil {
//		log.Errorf("Error starting %q", ControllerName)
//		return err
//	}
//
//	if debugHandler != nil && unsecuredMux != nil {
//		basePath := "/debug/controllers/" + ControllerName
//		unsecuredMux.UnlistedHandle(basePath, http.StripPrefix(basePath, debugHandler))
//		unsecuredMux.UnlistedHandlePrefix(basePath+"/", http.StripPrefix(basePath, debugHandler))
//	}
//	log.Infof("Started %q", ControllerName)
//	return nil
//}

// StartControllers starts a set of controllers with a specified ControllerContext
func StartControllers(ctx ControllerContext, controllers map[string]InitFunc, unsecuredMux *mux.PathRecorderMux) error {
	for controllerName, initFn := range controllers {
		time.Sleep(wait.Jitter(ctx.ComponentConfig.Generic.ControllerStartInterval.Duration, ControllerStartJitter))

		log.Infof("Starting %q", controllerName)
		debugHandler, err := initFn(ctx)
		if err != nil {
			log.Errorf("Error starting %q", controllerName)
			return err
		}

		if debugHandler != nil && unsecuredMux != nil {
			basePath := "/debug/controllers/" + controllerName
			unsecuredMux.UnlistedHandle(basePath, http.StripPrefix(basePath, debugHandler))
			unsecuredMux.UnlistedHandlePrefix(basePath+"/", http.StripPrefix(basePath, debugHandler))
		}
		log.Infof("Started %q", controllerName)
	}

	return nil
}

// NewControllerInitializers is a public map of named controller groups (you can start more than one in an init func)
// paired to their InitFunc.  This allows for structured downstream composition and subdivision.
func NewControllerInitializers() map[string]InitFunc {
	controllers := map[string]InitFunc{}
	controllers["polariscontroller"] = startPolarisController
	// controllers["accoutingcontroller"] = startPolarisAccountController

	return controllers
}

// ResyncPeriod returns a function which generates a duration each time it is
// invoked; this is so that multiple controllers don't get into lock-step and all
// hammer the apiserver with list requests simultaneously.
func ResyncPeriod(c *options.CompletedConfig) func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(c.ComponentConfig.Generic.MinResyncPeriod.Nanoseconds()) * factor)
	}
}

// buildAPI
func buildAPI() (api.ConsumerAPI, api.ProviderAPI, error) {
	cfg := api.NewConfiguration()

	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{polarisapi.PolarisGrpc})
	cfg.GetGlobal().GetServerConnector().SetConnectTimeout(time.Second * 10)
	cfg.GetGlobal().GetServerConnector().SetMessageTimeout(time.Second * 10)
	cfg.GetGlobal().GetAPI().SetTimeout(time.Second * 10)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetAddresses([]string{polarisapi.PolarisConfigGrpc})
	cfg.GetConfigFile().GetConfigConnectorConfig().SetConnectTimeout(time.Second * 10)
	cfg.GetConfigFile().GetConfigConnectorConfig().SetMessageTimeout(time.Second * 10)

	consumerAPI, err := api.NewConsumerAPIByConfig(cfg)
	if err != nil {
		log.Errorf("fail to create consumer with %s, err: %v", polarisapi.PolarisGrpc, err)
		return nil, nil, err
	}

	providerAPI, err := api.NewProviderAPIByConfig(cfg)
	if err != nil {
		log.Errorf("fail to create provider with %s, err: %v", polarisapi.PolarisGrpc, err)
		return nil, nil, err
	}

	return consumerAPI, providerAPI, nil
}

// startPolarisController
func startPolarisController(ctx ControllerContext) (http.Handler, error) {
	consumerAPI, providerAPI, err := buildAPI()
	if err != nil {
		return nil, err
	}

	handler, err := polarisController.NewPolarisController(
		ctx.InformerFactory.Core().V1().Pods(),
		ctx.InformerFactory.Core().V1().Services(),
		ctx.InformerFactory.Core().V1().Endpoints(),
		ctx.InformerFactory.Core().V1().Namespaces(),
		ctx.InformerFactory.Core().V1().ConfigMaps(),
		ctx.ClientBuilder.ClientOrDie(ControllerName),
		ctx.ComponentConfig,
		consumerAPI,
		providerAPI,
	)
	if err != nil {
		return nil, err
	}
	go handler.Run(ctx.ComponentConfig.PolarisController.ConcurrentPolarisSyncs, ctx.Stop)

	// register controller to server
	registerController(&ctx.ComponentConfig, providerAPI)

	// deregister controller from server
	go func() {
		defer deregisterController(&ctx.ComponentConfig, providerAPI)
		<-ctx.Stop
	}()

	return nil, nil
}

// startPolarisAccountController 启用反对账逻辑，定时从北极星侧拉取通过TKEx注册的服务，对比一下是否在集群中还存在
//func startPolarisAccountController(ctx ControllerContext) (http.Handler, error) {
//	go accounting_controller.NewPolarisAccountingController(
//		ctx.InformerFactory.Core().V1().Services(),
//		ctx.ClientBuilder.ClientOrDie(PolarisAccountingControllerName),
//		ctx.ComponentConfig,
//	).Run(ctx.ComponentConfig.PolarisController.ConcurrentPolarisSyncs, ctx.Stop)
//	return nil, nil
//}

// registerController register controller to server
func registerController(c *options.KubeControllerManagerConfiguration, provider api.ProviderAPI) {
	req := &api.InstanceRegisterRequest{}
	req.Service = "polaris-controller." + c.PolarisController.ClusterName
	req.ServiceToken = polarisapi.PolarisAccessToken
	req.Namespace = "Polaris"
	if podIP == "" {
		conn, err := net.Dial("ip4:1", common.PolarisServerAddress)
		if err != nil {
			log.Errorf("Sync: Failed to get controller ip address, %v", err)
		}
		defer conn.Close()

		podIP = conn.LocalAddr().(*net.IPAddr).IP.String()
	}
	req.Host = podIP
	req.Port = int(c.Generic.Port)
	req.SetTTL(5)
	// 开启自动上报心跳
	req.AutoHeartbeat = true

	_, err := provider.RegisterInstance(req)
	if err != nil {
		log.Errorf("Sync: Failed to register controller to polaris server, %v", err)
	}
}

// deregisterController deregister controller from server
func deregisterController(c *options.KubeControllerManagerConfiguration, provider api.ProviderAPI) {
	req := &api.InstanceDeRegisterRequest{}
	req.Service = "polaris-controller." + c.PolarisController.ClusterName
	req.Namespace = "Polaris"
	req.ServiceToken = polarisapi.PolarisAccessToken
	req.Host = podIP
	req.Port = int(c.Generic.Port)

	if err := provider.Deregister(req); err != nil {
		log.Errorf("Sync: Failed to deregister controller from polaris server, %v", err)
	}
}
