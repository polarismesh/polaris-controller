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

package controller

import (
	"strings"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"go.uber.org/atomic"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics/prometheus/ratelimiter"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	"github.com/polarismesh/polaris-controller/common/log"
	localCache "github.com/polarismesh/polaris-controller/pkg/cache"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

// PolarisController
type PolarisController struct {
	client           clientset.Interface
	eventBroadcaster record.EventBroadcaster
	eventRecorder    record.EventRecorder

	// serviceLister is able to list/get services and is populated by the shared informer passed to
	// NewEndpointController.
	serviceLister corelisters.ServiceLister
	// servicesSynced returns true if the service shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	servicesSynced cache.InformerSynced

	// podLister is able to list/get pods and is populated by the shared informer passed to
	// NewEndpointController.
	podLister corelisters.PodLister
	// podsSynced returns true if the pod shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	podsSynced cache.InformerSynced

	// endpointsLister is able to list/get endpoints and is populated by the shared informer passed to
	// NewEndpointController.
	endpointsLister corelisters.EndpointsLister
	// endpointsSynced returns true if the endpoints shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	endpointsSynced cache.InformerSynced

	namespaceLister corelisters.NamespaceLister
	namespaceSynced cache.InformerSynced

	configMapLister corelisters.ConfigMapLister
	configMapSynced cache.InformerSynced

	// Services that need to be updated. A channel is inappropriate here,
	// because it allows services with lots of pods to be serviced much
	// more often than services with few pods; it also would cause a
	// service that's inserted multiple times to be processed more than
	// necessary.
	queue workqueue.RateLimitingInterface

	// workerLoopPeriod is the time between worker runs. The workers process the queue of service and pod changes.
	workerLoopPeriod time.Duration

	// serviceCache 记录了service历史service相关信息
	serviceCache *localCache.CachedServiceMap
	// resyncServiceCache 需要进行同步的service历史
	resyncServiceCache *localCache.CachedServiceMap

	// configFileCache 记录了service历史service相关信息
	configFileCache *localCache.CachedConfigFileMap
	// resyncConfigFileCache 需要进行同步的service历史
	resyncConfigFileCache *localCache.CachedConfigFileMap

	consumer api.ConsumerAPI
	provider api.ProviderAPI

	// ComponentConfig provides access to init options for a given controller
	config options.KubeControllerManagerConfiguration

	// isPolarisServerHealthy indicates whether the polaris server is healthy
	// used to decide when to trigger a full resync
	isPolarisServerHealthy atomic.Bool

	// polarisServerFailedTimes record the failed time of polaris server which is used to trigger full resync
	polarisServerFailedTimes int
}

const (
	polarisControllerName       = "polaris-controller"
	maxRetries                  = 10
	metricPolarisControllerName = "polaris_controller"
	noAllow                     = "false"
	polarisEvent                = "PolarisRegister"

	// ServiceKeyFlagAdd 加在 queue 中的 key 后，表示 key 对应的 service ，处理时，是否是北极星要处理的服务。
	// 后续处理流程 syncService 方法中，通过这个字段判断是否要删除服务实例
	ServiceKeyFlagAdd    = "add"
	ServiceKeyFlagDelete = "delete"
)

// NewPolarisController
func NewPolarisController(
	podInformer coreinformers.PodInformer,
	serviceInformer coreinformers.ServiceInformer,
	endpointsInformer coreinformers.EndpointsInformer,
	namespaceInformer coreinformers.NamespaceInformer,
	configmapInformer coreinformers.ConfigMapInformer,
	client clientset.Interface,
	config options.KubeControllerManagerConfiguration,
	consumerAPI api.ConsumerAPI,
	providerAPI api.ProviderAPI,
) (*PolarisController, error) {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(log.Infof)
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: client.CoreV1().Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: polarisControllerName})
	if client != nil && client.CoreV1().RESTClient().GetRateLimiter() != nil {
		_ = ratelimiter.RegisterMetricAndTrackRateLimiterUsage(
			metricPolarisControllerName, client.CoreV1().RESTClient().GetRateLimiter())
	}

	metrics.RegisterMetrics()

	p := &PolarisController{
		client: client,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(),
			polarisControllerName),
		workerLoopPeriod: time.Second,
	}

	serviceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onServiceAdd,
		UpdateFunc: p.onServiceUpdate,
		DeleteFunc: p.onServiceDelete,
	})

	endpointsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onEndpointAdd,
		UpdateFunc: p.onEndpointUpdate,
		DeleteFunc: p.onEndpointDelete,
	})

	namespaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onNamespaceAdd,
		UpdateFunc: p.onNamespaceUpdate,
	})

	configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onConfigMapAdd,
		UpdateFunc: p.onConfigMapUpdate,
		DeleteFunc: p.onConfigMapDelete,
	})

	p.serviceLister = serviceInformer.Lister()
	p.servicesSynced = serviceInformer.Informer().HasSynced

	p.podLister = podInformer.Lister()
	p.podsSynced = podInformer.Informer().HasSynced

	p.endpointsLister = endpointsInformer.Lister()
	p.endpointsSynced = endpointsInformer.Informer().HasSynced

	p.namespaceLister = namespaceInformer.Lister()
	p.namespaceSynced = namespaceInformer.Informer().HasSynced

	p.configMapLister = configmapInformer.Lister()
	p.configMapSynced = configmapInformer.Informer().HasSynced

	p.eventBroadcaster = broadcaster
	p.eventRecorder = recorder

	p.serviceCache = localCache.NewCachedServiceMap()
	p.resyncServiceCache = localCache.NewCachedServiceMap()
	p.isPolarisServerHealthy.Store(true)

	p.consumer = consumerAPI
	p.provider = providerAPI

	p.config = config
	return p, nil
}

// Run will not return until stopCh is closed. workers determines how many
// endpoints will be handled in parallel.
func (p *PolarisController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer p.queue.ShutDown()
	defer p.consumer.Destroy()
	defer p.provider.Destroy()

	defer log.Infof("Shutting down polaris controller")

	if !cache.WaitForCacheSync(stopCh, p.podsSynced, p.servicesSynced, p.endpointsSynced, p.namespaceSynced) {
		return
	}

	p.CounterPolarisService()

	for i := 0; i < workers; i++ {
		go wait.Until(p.worker, p.workerLoopPeriod, stopCh)
	}

	// 定时任务
	go p.MetricTracker(stopCh)

	// 定时对账
	if p.config.PolarisController.ResyncDuration != 0 {
		go wait.Until(p.resyncWorker, p.config.PolarisController.ResyncDuration, stopCh)
	} else {
		go wait.Until(p.resyncWorker, time.Second*30, stopCh)
	}

	// 定时健康探测
	if p.config.PolarisController.HealthCheckDuration != 0 {
		go wait.Until(p.checkHealth, p.config.PolarisController.HealthCheckDuration, stopCh)
	} else {
		go wait.Until(p.checkHealth, time.Second, stopCh)
	}

	<-stopCh
}

func (p *PolarisController) worker() {
	for p.processNextWorkItem() {
	}
}

func (p *PolarisController) processNextWorkItem() bool {
	key, quit := p.queue.Get()
	if quit {
		return false
	}
	defer p.queue.Done(key)

	var err error
	if !strings.Contains(key.(string), "/") {
		// deal with namespace
		err = p.syncNamespace(key.(string))
	} else {
		err = p.syncService(key.(string))
	}

	p.handleErr(err, key)

	return true
}

func (p *PolarisController) handleErr(err error, key interface{}) {
	if err == nil {
		p.queue.Forget(key)
		return
	}

	if p.queue.NumRequeues(key) < maxRetries {
		log.Errorf("Error syncing service %q, retrying. Error: %v", key, err)
		p.queue.AddRateLimited(key)
		return
	}

	log.Warnf("Dropping service %q out of the queue: %v", key, err)
	p.queue.Forget(key)
	runtime.HandleError(err)
}

// CounterPolarisService
func (p *PolarisController) CounterPolarisService() {
	serviceList, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		log.Errorf("Failed to get service list %v", serviceList)
		return
	}
	metrics.PolarisCount.Reset()
	for _, service := range serviceList {
		if util.IsPolarisService(service, p.config.PolarisController.SyncMode) {
			polarisNamespace := service.Namespace
			polarisService := service.Name
			metrics.PolarisCount.
				WithLabelValues(service.GetNamespace(), service.GetName(), polarisNamespace, polarisService).Set(1)
		}
	}
}

// MetricTracker
func (p *PolarisController) MetricTracker(stopCh <-chan struct{}) {
	ticker := time.NewTicker(time.Second * 30)
	for range ticker.C {
		p.CounterPolarisService()
	}
	<-stopCh
}
