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
	"context"
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"go.uber.org/atomic"
	"go.uber.org/zap"
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

	// aliasLocks serializes reconciliation for the same Polaris alias.
	aliasLocks sync.Map
	// aliasClaimStates deduplicates repeated waiting/conflict events.
	aliasClaimStates sync.Map

	syncStateStore *syncStateStore

	// workerLoopPeriod is the time between worker runs. The workers process the queue of service and pod changes.
	workerLoopPeriod time.Duration

	// appliedServiceCache stores the last Service state successfully applied to Polaris.
	appliedServiceCache *localCache.CachedServiceMap
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

	// polarisConfigWatcher 负责将北极星上的配置同步到 kubernetes ConfigMap
	polarisConfigWatcher *PolarisConfigWatcher
}

const (
	polarisControllerName       = "polaris-controller"
	maxRetries                  = 10
	metricPolarisControllerName = "polaris_controller"
	polarisEvent                = "PolarisRegister"
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

	metrics.RegisterMetrics()

	p := &PolarisController{
		client: client,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(),
			polarisControllerName),
		workerLoopPeriod: time.Second,
		syncStateStore:   newSyncStateStore(client),
	}

	_, _ = serviceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onServiceAdd,
		UpdateFunc: p.onServiceUpdate,
		DeleteFunc: p.onServiceDelete,
	})

	_, _ = endpointsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onEndpointAdd,
		UpdateFunc: p.onEndpointUpdate,
		DeleteFunc: p.onEndpointDelete,
	})

	_, _ = namespaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
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

	_, _ = configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    p.onConfigMapAdd,
		UpdateFunc: p.onConfigMapUpdate,
		DeleteFunc: p.onConfigMapDelete,
	})
	p.configMapLister = configmapInformer.Lister()
	p.configMapSynced = configmapInformer.Informer().HasSynced

	p.eventBroadcaster = broadcaster
	p.eventRecorder = recorder

	p.appliedServiceCache = localCache.NewCachedServiceMap()
	p.resyncServiceCache = localCache.NewCachedServiceMap()
	p.configFileCache = localCache.NewCachedConfigFileMap()
	p.resyncConfigFileCache = localCache.NewCachedConfigFileMap()
	p.isPolarisServerHealthy.Store(true)

	p.consumer = consumerAPI
	p.provider = providerAPI

	p.config = config
	log.Info("controller config", zap.Any("info", p.config.PolarisController))
	return p, p.watchPolarisConfig()
}

// AllowSyncFromConfigMap 是否开启配置同步从 ConfigMap 到 Polaris
func (p *PolarisController) AllowSyncFromConfigMap() bool {
	syncDirection := p.config.PolarisController.ConfigSync.SyncDirection
	allowSyn := syncDirection == util.SyncDirectionKubernetesToPolaris || syncDirection == util.SyncDirectionBoth
	return p.config.PolarisController.ConfigSync.Enable && allowSyn
}

// AllowSyncToConfigMap 是否开启配置同步从 Polaris 到 Kubernetes
func (p *PolarisController) AllowSyncToConfigMap() bool {
	syncDirection := p.config.PolarisController.ConfigSync.SyncDirection
	allowSync := syncDirection == util.SyncDirectionPolarisToKubernetes || syncDirection == util.SyncDirectionBoth
	return p.config.PolarisController.ConfigSync.Enable && allowSync
}

func (p *PolarisController) allowDeleteConfig() bool {
	return p.config.PolarisController.ConfigSync.AllowDelete
}

// Run will not return until stopCh is closed. workers determines how many
// endpoints will be handled in parallel.
func (p *PolarisController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer p.queue.ShutDown()
	defer p.consumer.Destroy()
	defer p.provider.Destroy()

	defer log.Infof("Shutting down polaris controller")

	if !cache.WaitForCacheSync(stopCh, p.podsSynced, p.servicesSynced, p.endpointsSynced,
		p.namespaceSynced, p.configMapSynced) {
		return
	}
	if err := p.recoverSyncStates(context.Background()); err != nil {
		log.Errorf("failed to recover persisted sync states: %v", err)
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
		if !p.handleTask(p.process) {
			return
		}
	}
}

func (p *PolarisController) process(t *Task) error {
	var err error
	switch t.ObjectType {
	case KubernetesNamespace:
		// deal with namespace
		err = p.syncNamespace(t.Namespace)
	case KubernetesService, KubernetesEndpoints:
		err = p.syncService(t)
	case KubernetesConfigMap:
		err = p.syncConfigMap(t)
	case PolarisSyncState:
		err = p.processPendingSyncState(t)
	}
	return err
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
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			p.CounterPolarisService()
		}
	}
}

// IsNamespaceSyncEnable 命名空间是否启用了 sync 注解
func (p *PolarisController) IsNamespaceSyncEnable(ns *v1.Namespace) bool {
	syncEnabled, ok := ns.Annotations[util.PolarisSync]
	switch p.SyncMode() {
	case util.SyncModeDemand:
		return syncEnabled == util.IsEnableSync
	default:
		if !ok {
			return true
		}
		return syncEnabled == util.IsEnableSync
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

	syncEnabled, ok := svc.Annotations[util.PolarisSync]
	// 优先看服务的 annotation 中是否携带 polarismesh.cn/sync 注解
	if ok {
		return syncEnabled == util.IsEnableSync
	}
	// enableRegister 是历史兼容的同步开关：仅在 sync 缺失时生效。
	// 存量 Service 只设置该注解，且依赖 controller 正常注册实例。
	if svc.Annotations[util.PolarisEnableRegister] == util.IsEnableSync {
		return true
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
	if !p.IsPolarisService(new) {
		return util.ServicePolarisDelete
	}
	return util.CompareServiceAnnotationsChange(old.GetAnnotations(), new.GetAnnotations())
}

// IsPolarisConfigMap 用于判断是是否满足创建 PolarisConfigMap 的要求字段，这块逻辑应该在webhook中也增加
func (p *PolarisController) IsPolarisConfigMap(svc *v1.ConfigMap) bool {

	// 过滤一些不合法的对象
	if util.IgnoreObject(svc) {
		return false
	}
	if svc.GetLabels()[syncStateLabelKey] == syncStateLabelValue {
		return false
	}
	if val, ok := svc.Annotations[util.InternalConfigFileSyncSourceKey]; ok {
		// 如果同步来源是 polaris, 则不能在认为是需要同步回 polaris 的 ConfigMap
		if val == util.SourceFromPolaris {
			return false
		}
	}

	syncEnabled, ok := svc.Annotations[util.PolarisSync]
	// 优先看 ConfigMap 的 annotation 中是否携带 polarismesh.cn/sync 注解
	if ok {
		return syncEnabled == util.IsEnableSync
	}
	ns, err := p.namespaceLister.Get(svc.GetNamespace())
	if err != nil {
		log.SyncConfigScope().Errorf("get namespace for ConfigMap %s/%s error, %v", svc.Namespace, svc.Name, err)
		return false
	}
	// 如果服务没有注解，则在看下命名空间是否有开启相关 sync 注解
	return p.IsNamespaceSyncEnable(ns)
}

func (p *PolarisController) insertTask(t *Task) {
	switch t.ObjectType {
	case KubernetesConfigMap:
		log.SyncConfigScope().Infof("insert task: %s", t.String())
	case KubernetesService, KubernetesEndpoints, PolarisSyncState:
		log.SyncNamingScope().Infof("insert task: %s", t.String())
	case KubernetesNamespace:
		log.Infof("insert task: %s", t.String())
	}
	log.TraceScope().Info("INSERT-TASK|" + t.String())
	p.queue.Add(t.QueueKey())
}

func (p *PolarisController) handleTask(f func(t *Task) error) bool {
	val, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(val)
	task := val.(taskQueueKey).task()
	log.TraceScope().Info("HANDLE-TASK|" + task.String())
	err := f(task)
	if err == nil {
		log.TraceScope().Info("REMOVE-TASK|" + task.String())
		p.queue.Forget(val)
		return true
	}

	if p.queue.NumRequeues(val) < maxRetries {
		log.TraceScope().Error("RETRY-TASK|" + task.String())
		log.Errorf("processing task %v, retrying. Error: %v", task, err)
		p.queue.AddRateLimited(val)
		return true
	}

	log.Errorf("Dropping task %v out of the queue: %v", task, err)
	log.TraceScope().Error("DROP-TASK|" + task.String())
	p.queue.Forget(val)
	runtime.HandleError(err)
	return true
}
