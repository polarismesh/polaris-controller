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

	// ResourceKeyFlagAdd 加在 queue 中的 key 后，表示 key 对应的 service ，处理时，是否是北极星要处理的服务。
	// 后续处理流程 syncService 方法中，通过这个字段判断是否要删除服务实例
	ResourceKeyFlagAdd    = "add"
	ResourceKeyFlagDelete = "delete"
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

	p.serviceLister = serviceInformer.Lister()
	p.servicesSynced = serviceInformer.Informer().HasSynced

	p.podLister = podInformer.Lister()
	p.podsSynced = podInformer.Informer().HasSynced

	p.endpointsLister = endpointsInformer.Lister()
	p.endpointsSynced = endpointsInformer.Informer().HasSynced

	p.namespaceLister = namespaceInformer.Lister()
	p.namespaceSynced = namespaceInformer.Informer().HasSynced

	p.eventBroadcaster = broadcaster
	p.eventRecorder = recorder

	p.serviceCache = localCache.NewCachedServiceMap()
	p.resyncServiceCache = localCache.NewCachedServiceMap()
	p.isPolarisServerHealthy.Store(true)

	if p.OpenSyncConfigMap() {
		configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    p.onConfigMapAdd,
			UpdateFunc: p.onConfigMapUpdate,
			DeleteFunc: p.onConfigMapDelete,
		})
		p.configMapLister = configmapInformer.Lister()
		p.configMapSynced = configmapInformer.Informer().HasSynced
	}

	p.consumer = consumerAPI
	p.provider = providerAPI

	p.config = config
	return p, nil
}

func (p *PolarisController) OpenSyncConfigMap() bool {
	return p.config.PolarisController.SyncConfigMap
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
	if p.OpenSyncConfigMap() {
		if !cache.WaitForCacheSync(stopCh, p.configMapSynced) {
			return
		}
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
	for {
		if !p.handleTask(p.proces) {
			return
		}
	}
}

func (p *PolarisController) proces(t *Task) error {
	var err error
	switch t.ObjectType {
	case KubernetesNamespace:
		// deal with namespace
		err = p.syncNamespace(t.Namespace)
	case KubernetesService:
		err = p.syncService(t)
	case KubernetesConfigMap:
		err = p.syncConfigMap(t)
	}
	return err
}

func (p *PolarisController) handleErr(err error, task *Task) {

}

// CounterPolarisService
func (p *PolarisController) CounterPolarisService() {
	serviceList, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		log.Errorf("Failed to get service list %v", err)
		return
	}
	metrics.PolarisCount.Reset()
	for _, service := range serviceList {
		if p.IsPolarisService(service) {
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

// IsNamespaceSyncEnable 命名空间是否启用了 sync 注解
func (p *PolarisController) IsNamespaceSyncEnable(ns *v1.Namespace) bool {
	sync, ok := ns.Annotations[util.PolarisSync]
	switch p.SyncMode() {
	case util.SyncModeDemand:
		return sync == util.IsEnableSync
	default:
		if !ok {
			return true
		}
		return sync == util.IsEnableSync
	}
}

func (p *PolarisController) SyncMode() string {
	return p.config.PolarisController.SyncMode
}

// IsPolarisService 用于判断是是否满足创建PolarisService的要求字段，这块逻辑应该在webhook中也增加
func (p *PolarisController) IsPolarisService(svc *v1.Service) bool {
	// 过滤一些不合法的 service
	if util.IgnoreService(svc) {
		return false
	}

	sync, ok := svc.Annotations[util.PolarisSync]
	// 优先看服务的 annotation 中是否携带 polarismesh.cn/sync 注解
	if ok {
		return sync == util.IsEnableSync
	}
	ns, err := p.namespaceLister.Get(svc.Namespace)
	if err != nil {
		log.SyncNamingScope().Errorf("get namespace for service %s/%s error, %v", svc.Namespace, svc.Name, err)
		return false
	}
	// 如果服务没有注解，则在看下命名空间是否有开启相关 sync 注解
	return p.IsNamespaceSyncEnable(ns)
}

// CompareServiceChange 判断本次更新是什么类型的
func (p *PolarisController) CompareServiceChange(old, new *v1.Service) util.ServiceChangeType {

	log.Infof("CompareServiceChange new is %v", new.Annotations)

	if !p.IsPolarisService(new) {
		return util.ServicePolarisDelete
	}
	return util.CompareServiceAnnotationsChange(old.GetAnnotations(), new.GetAnnotations())
}

// IsPolarisConfigMap 用于判断是是否满足创建 PolarisConfigMap 的要求字段，这块逻辑应该在webhook中也增加
func (p *PolarisController) IsPolarisConfigMap(svc *v1.ConfigMap) bool {

	// 过滤一些不合法的 service
	if util.IgnoreObject(svc) {
		return false
	}

	sync, ok := svc.Annotations[util.PolarisSync]
	// 优先看服务的 annotation 中是否携带 polarismesh.cn/sync 注解
	if ok {
		return sync == util.IsEnableSync
	}
	ns, err := p.namespaceLister.Get(svc.Namespace)
	if err != nil {
		log.SyncConfigScope().Errorf("get namespace for service %s/%s error, %v", svc.Namespace, svc.Name, err)
		return false
	}
	// 如果服务没有注解，则在看下命名空间是否有开启相关 sync 注解
	return p.IsNamespaceSyncEnable(ns)
}

func (p *PolarisController) insertTask(t *Task) {
	p.queue.Add(t)
}

func (p *PolarisController) handleTask(f func(t *Task) error) bool {
	val, shutdown := p.queue.Get()
	if shutdown {
		return shutdown
	}
	defer p.queue.Done(val)
	task := val.(*Task)
	err := f(task)
	if err == nil {
		p.queue.Forget(val)
		return true
	}

	if p.queue.NumRequeues(val) < maxRetries {
		log.Infof("syncing service %v, retrying. Error: %v", task, err)
		p.queue.AddRateLimited(val)
		return true
	}

	log.Errorf("Dropping service %v out of the queue: %v", task, err)
	p.queue.Forget(val)
	runtime.HandleError(err)
	return true
}
