/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package controller

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	"github.com/polarismesh/polaris-controller/common/log"
	localCache "github.com/polarismesh/polaris-controller/pkg/cache"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
	"github.com/polarismesh/polaris-go/api"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
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

	consumer api.ConsumerAPI
	provider api.ProviderAPI

	// ComponentConfig provides access to init options for a given controller
	config options.KubeControllerManagerConfiguration
}

const (
	polarisControllerName       = "polaris-controller"
	maxRetries                  = 10
	metricPolarisControllerName = "polaris_controller"
	noEnableRegister            = "false"
	polarisEvent                = "PolarisRegister"

	// ServiceKeyFlagAdd 加在 queue 中的 key 后，表示 key 对应的 service ，处理时，是否是北极星要处理的服务。
	// 后续处理流程 syncService 方法中，通过这个字段判断是否要删除服务实例
	ServiceKeyFlagAdd    = "add"
	ServiceKeyFlagDelete = "delete"
)

// NewPolarisController
func NewPolarisController(podInformer coreinformers.PodInformer,
	serviceInformer coreinformers.ServiceInformer,
	endpointsInformer coreinformers.EndpointsInformer,
	namespaceInformer coreinformers.NamespaceInformer,
	client clientset.Interface,
	config options.KubeControllerManagerConfiguration) *PolarisController {
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

	cfg := api.NewConfiguration()

	cfg.GetGlobal().GetServerConnector().SetAddresses([]string{polarisapi.PolarisGrpc})
	cfg.GetGlobal().GetServerConnector().SetConnectTimeout(time.Second * 10)
	cfg.GetGlobal().GetServerConnector().SetMessageTimeout(time.Second * 10)
	cfg.GetGlobal().GetAPI().SetTimeout(time.Second * 10)

	p.consumer, _ = api.NewConsumerAPIByConfig(cfg)
	//获取默认配置
	p.provider, _ = api.NewProviderAPI()

	p.config = config
	return p
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

	//定时任务
	go p.MetricTracker(stopCh)

	<-stopCh
}

// onServiceUpdate 比较是否有必要加入service队列
func (p *PolarisController) onServiceUpdate(old, current interface{}) {
	oldService, ok1 := old.(*v1.Service)
	curService, ok2 := current.(*v1.Service)

	if !(ok1 && ok2) {
		log.Errorf("Error get update services old %v, new %v", old, current)
		return
	}

	key, err := util.GenServiceQueueKey(curService)
	if err != nil {
		log.Errorf("generate service queue key in onServiceUpdate error, %v", err)
		return
	}

	// 为什么不适用这个做对比？ 因为可以利用re sync做同步对账，如果对比version，还要自在写同步逻辑
	//if ok1 && ok2 && oldService.GetResourceVersion() == curService.GetResourceVersion() {
	//	return
	//}

	log.Infof("Service %s/%s is update", curService.GetNamespace(), curService.GetName())

	// 如果是按需同步，则处理一下 sync 为 空 -> sync 为 false 的场景，需要删除。
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsServiceHasSyncAnnotation(oldService) && util.IsServiceSyncDisable(curService) {
			log.Infof("Service %s is update because sync no to false", key)
			metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
			p.queue.Add(key)
			return
		}
	}

	// 这里需要确认是否加入svc进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := util.IsPolarisService(oldService, p.config.PolarisController.SyncMode)
	curIsPolaris := util.IsPolarisService(curService, p.config.PolarisController.SyncMode)
	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		log.Infof("Service %s is update polaris config", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
		p.serviceCache.Store(key, oldService)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		log.Infof("Service %s is update to polaris type", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
	}
}

// onServiceAdd 在识别到是这个北极星类型service的时候进行处理
func (p *PolarisController) onServiceAdd(obj interface{}) {

	service := obj.(*v1.Service)

	if !util.IsPolarisService(service, p.config.PolarisController.SyncMode) {
		return
	}

	key, err := util.GenServiceQueueKey(service)
	if err != nil {
		log.Errorf("generate queue key for %s/%s error, %v", service.Namespace, service.Name, err)
		return
	}

	p.enqueueService(key, service, "Add")
}

// onServiceDelete 在识别到是这个北极星类型service删除的时候，才进行处理
// 判断这个是否是北极星service，如果是北极星service，就加入队列。
func (p *PolarisController) onServiceDelete(obj interface{}) {

	service := obj.(*v1.Service)

	key, err := util.GenServiceQueueKey(service)
	if err != nil {
		log.Errorf("generate queue key for %s/%s error, %v", service.Namespace, service.Name, err)
		return
	}

	// demand 模式中。 删除服务时，如果 ns 的 sync 为 true ，则在 service 流程里补偿处理，因为 ns 流程感知不到 service 删除
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		ns, err := p.namespaceLister.Get(service.Namespace)
		if err != nil {
			log.Errorf("get namespace for service %s/%s error, %v", service.Namespace, service.Name, err)
			return
		}
		if util.IsNamespaceSyncEnable(ns) {
			log.Infof("Service %s is polaris type, in queue", key)
			metrics.SyncTimes.WithLabelValues("Delete", "Service").Inc()
			p.queue.Add(key)
		}
	}

	if !util.IsPolarisService(service, p.config.PolarisController.SyncMode) {
		return
	}

	p.enqueueService(key, service, "Delete")
}

func (p *PolarisController) onEndpointAdd(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoint := obj.(*v1.Endpoints)

	if !util.IgnoreEndpoint(endpoint) {
		log.Infof("Endpoint %s/%s in ignore namespaces", endpoint.Name, endpoint.Namespace)
		return
	}

	key, err := util.GenObjectQueueKey(endpoint)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error %v", endpoint.Namespace, endpoint.Name, err)
		return
	}

	// 如果是 demand 模式，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(endpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", endpoint.Namespace, endpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	p.enqueueEndpoint(key, endpoint, "Add")
}

func (p *PolarisController) onNamespaceAdd(obj interface{}) {

	namespace := obj.(*v1.Namespace)

	if !util.IgnoreNamespace(namespace) {
		log.Infof("%s in ignore namespaces", namespace.Name)
		return
	}

	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		// 过滤掉不需要处理 ns
		if !util.IsNamespaceSyncEnable(namespace) {
			return
		}
	}

	key, err := util.GenObjectQueueKey(namespace)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", namespace, err))
		return
	}

	p.enqueueNamespace(key, namespace)
}

func (p *PolarisController) onNamespaceUpdate(old, cur interface{}) {

	oldNs := old.(*v1.Namespace)
	curNs := cur.(*v1.Namespace)

	if !util.IgnoreNamespace(oldNs) {
		log.Infof("ignore namespaces %s", oldNs.Name)
		return
	}

	nsKey, err := util.GenObjectQueueKey(curNs)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", curNs, err))
		return
	}

	// 处理 namespace
	if !util.IsNamespaceSyncEnable(oldNs) && util.IsNamespaceSyncEnable(curNs) {
		p.enqueueNamespace(nsKey, curNs)
	}

	if p.config.PolarisController.SyncMode == util.SyncModeDemand {

		// 有几种情况：
		// 1. 无 sync -> 无 sync，不处理 ns 和 service
		// 2. 有 sync -> 有 sync，将 ns 下 service 加入队列，标志为 polaris 要处理的，即添加
		// 3. 无 sync -> 有 sync，将 ns 下 service 加入队列，标志为 polaris 要处理的，即添加
		// 4. 有 sync -> 无 sync，将 ns 下 service 加入队列，标志为 polaris 不需要处理的，即删除

		isOldSync := util.IsNamespaceSyncEnable(oldNs)
		isCurSync := util.IsNamespaceSyncEnable(curNs)

		operation := ""
		if !isOldSync && !isCurSync {
			// 情况 1
			return
		} else if isCurSync {
			// 情况 2、3
			operation = ServiceKeyFlagAdd
		} else {
			// 情况 4
			operation = ServiceKeyFlagDelete
		}

		services, err := p.serviceLister.Services(oldNs.Name).List(labels.Everything())
		if err != nil {
			log.Errorf("get namespaces %s services error in onNamespaceUpdate, %v\n", curNs.Name, err)
			return
		}

		log.Infof("namespace %s operation is %s", curNs.Name, operation)

		for _, service := range services {
			// 非法的 service 不处理
			if util.IgnoreService(service) {
				continue
			}
			// service 确定有 sync=true 标签的，不需要在这里投入队列。由后续 service 事件流程处理，减少一些冗余。
			if util.IsServiceSyncEnable(service) {
				log.Infof("service %s/%s is enabled", service.Namespace, service.Name)
				continue
			}

			// namespace 当前有 sync ，且 service sync 为 false 的场景，namespace 流程不处理， service 流程来处理。
			// 如果由 namespace 流程处理，则每次 resync ，多需要处理一次
			if operation == ServiceKeyFlagAdd && util.IsServiceSyncDisable(service) {
				continue
			}

			key, err := util.GenServiceQueueKeyWithFlag(service, operation)
			if err != nil {
				log.Errorf("get key from service [%s/%s] in onNamespaceUpdate error, %v\n", oldNs, service.Name, err)
				continue
			}
			p.queue.Add(key)
		}
	}
}

func (p *PolarisController) onEndpointUpdate(old, cur interface{}) {

	// 先确认service是否是Polaris的，后再做比较，会提高效率。
	oldEndpoint, ok1 := old.(*v1.Endpoints)
	curEndpoint, ok2 := cur.(*v1.Endpoints)

	// 过滤非法的 endpoints
	if !util.IgnoreEndpoint(curEndpoint) {
		return
	}

	key, err := util.GenObjectQueueKey(old)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error, %v", oldEndpoint.Name, oldEndpoint.Namespace, err)
		return
	}

	// demand 模式下，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(curEndpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", curEndpoint.Namespace, curEndpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	// 这里有不严谨的情况， endpoint update 时的service有从
	// 1. polaris -> not polaris
	// 2. not polaris -> polaris
	// 3. 只变更 endpoint.subsets
	if ok1 && ok2 && !reflect.DeepEqual(oldEndpoint.Subsets, curEndpoint.Subsets) {
		metrics.SyncTimes.WithLabelValues("Update", "Endpoint").Inc()
		log.Infof("Endpoints %s is updating, in queue", key)
		p.queue.Add(key)
	}
}

// isEpServiceNsOrSyncEnable endpoints 的 service 和 namespace 是否带有 sync 注解。
// 返回是否了注解，如果带了注解，会返回应该加入到处理队列中的 key。
// 这里有几种情况：
// 1. service sync = true ，要处理
// 2. service sync 为 false， 不处理
// 3. service sync 为空， namespace sync = true ，要处理
// 4. service sync 为空， namespace sync 为空或者 false ，不处理
func (p *PolarisController) isPolarisEndpoints(endpoint *v1.Endpoints) (bool, string, error) {

	// 先检查 endpoints 的 service 上是否有注解
	service, err := p.serviceLister.Services(endpoint.GetNamespace()).Get(endpoint.GetName())
	if err != nil {
		log.Errorf("Unable to find the service of the endpoint %s/%s, %v",
			endpoint.Name, endpoint.Namespace, err)
		return false, "", err
	}

	if util.IsServiceSyncEnable(service) {
		// 情况 1 ，要处理
		key, err := util.GenServiceQueueKey(service)
		if err != nil {
			log.Errorf("get service %s/%s key in enqueueEndpoint error, %v", service.Name, service.Namespace, err)
			return false, "", err
		}
		return true, key, nil
	} else {
		// 检查 service 是否有 false 注解
		if util.IsServiceSyncDisable(service) {
			// 情况 2 ，不处理
			return false, "", nil
		}
		// 再检查 namespace 上是否有注解
		namespace, err := p.namespaceLister.Get(service.Namespace)
		if err != nil {
			log.Errorf("Unable to find the namespace of the endpoint %s/%s, %v",
				endpoint.Name, endpoint.Namespace, err)
			return false, "", err
		}
		if util.IsNamespaceSyncEnable(namespace) {
			// 情况 3 ，要处理
			key, err := util.GenServiceQueueKeyWithFlag(service, ServiceKeyFlagAdd)
			if err != nil {
				log.Errorf("Unable to find the key of the service %s/%s, %v",
					service.Name, service.Namespace, err)
			}
			return true, key, nil
		}
	}

	return false, "", nil
}

func (p *PolarisController) onEndpointDelete(obj interface{}) {
	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoint := obj.(*v1.Endpoints)

	if !util.IgnoreEndpoint(endpoint) {
		log.Infof("Endpoint %s/%s in ignore namespaces", endpoint.Name, endpoint.Namespace)
		return
	}

	key, err := util.GenObjectQueueKey(endpoint)
	if err != nil {
		log.Errorf("get object key for endpoint %s/%s error %v", endpoint.Namespace, endpoint.Name, err)
		return
	}

	// 如果是 demand 模式，检查 endpoint 的 service 和 namespace 上是否有注解
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		sync, k, err := p.isPolarisEndpoints(endpoint)
		if err != nil {
			log.Errorf("check if ep %s/%s svc/ns has sync error, %v", endpoint.Namespace, endpoint.Name, err)
			return
		}
		// 没有注解，不处理
		if !sync {
			return
		}
		key = k
	}

	p.enqueueEndpoint(key, endpoint, "Delete")
}

func (p *PolarisController) enqueueNamespace(key string, namespace *v1.Namespace) {

	p.queue.Add(key)
}

func (p *PolarisController) enqueueEndpoint(key string, endpoint *v1.Endpoints, eventType string) {

	log.Infof("Endpoint %s is polaris, in queue", key)
	metrics.SyncTimes.WithLabelValues(eventType, "Endpoint").Inc()
	p.queue.Add(key)
}

func (p *PolarisController) enqueueService(key string, service *v1.Service, eventType string) {

	log.Infof("Service %s is polaris type, in queue", key)
	metrics.SyncTimes.WithLabelValues(eventType, "Service").Inc()
	p.queue.Add(key)
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

func (p *PolarisController) syncNamespace(key string) error {

	log.Infof("Begin to sync namespaces %s", key)

	createNsResponse, err := polarisapi.CreateNamespaces(key)
	if err != nil {
		log.Errorf("Failed create namespaces in syncNamespace %s, err %s, resp %v",
			key, err, createNsResponse)
		return err
	}

	return nil
}

func (p *PolarisController) syncService(key string) error {
	log.Infof("Start sync service %s, queue deep %d", key, p.queue.Len())
	startTime := time.Now()
	defer func() {
		log.Infof("Finished syncing service %q service. (%v)", key, time.Since(startTime))
	}()

	realKey, namespaces, name, op, err := util.GetServiceRealKeyWithFlag(key)
	if err != nil {
		log.Errorf("GetServiceRealKeyWithFlag %s error, %v", key, err)
		return err
	}

	service, err := p.serviceLister.Services(namespaces).Get(name)
	switch {
	case errors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedService, ok := p.serviceCache.Load(realKey)
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			log.Errorf("Service %s not in cache even though the watcher thought it was. Ignoring the deletion", realKey)
			return nil
		}
		log.Infof("Service %s is in cache, cache info %v", realKey, cachedService.Name)
		// 查询原svc是啥，删除对应实例
		processError := p.processDeleteService(cachedService)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedService, v1.EventTypeWarning, polarisEvent, "Delete polaris instances failed %v",
				processError)
			return processError
		}
		p.serviceCache.Delete(realKey)
	case err != nil:
		log.Errorf("Unable to retrieve service %v from store: %v", realKey, err)
	default:
		// 条件判断将会增加
		// 1. 首次创建service
		// 2. 更新service
		//    a. 北极星相关信息不变，更新了对应的metadata,ttl,权重信息
		//    b. 北极星相关信息改变，相当于删除旧的北极星信息，注册新的北极星信息。
		log.Infof("Begin to process service %s, operation is %s", realKey, op)

		operationService := service

		// 如果是 namespace 流程传来的任务，且 op 为 add ，为 service 打上 sync 标签
		if op == ServiceKeyFlagAdd {
			operationService = service.DeepCopy()
			v12.SetMetaDataAnnotation(&operationService.ObjectMeta, util.PolarisSync, util.IsEnableSync)
		}

		cachedService, ok := p.serviceCache.Load(realKey)
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			log.Infof("Service %s not in cache, begin to add new polaris", realKey)

			// 同步 k8s 的 namespace 和 service 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndService(operationService); processError != nil {
				log.Errorf("ns/svc %s sync failed", realKey)
				return processError
			}

			if !util.IsPolarisService(operationService, p.config.PolarisController.SyncMode) {
				// 不合法的 service ，只把 service 同步到北极星，不做后续的实例处理
				log.Infof("service %s is not valid, do not process ", realKey)
				return nil
			}

			if processError := p.processSyncInstance(operationService); processError != nil {
				log.Errorf("Service %s first add failed", realKey)
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateService(cachedService, operationService); processError != nil {
				log.Errorf("Service %s update add failed", realKey)
				return processError
			}
		}
		p.serviceCache.Store(realKey, service)
	}
	return nil
}

func (p *PolarisController) processDeleteService(service *v1.Service) (err error) {

	log.Infof("Delete Service %s/%s", service.GetNamespace(), service.GetName())

	instances, err := p.getAllInstance(service)
	if err != nil {
		return err
	}
	// 筛选出来只属于这个service的IP
	// 更简单的做法，直接从cachedEndpoint删除，处理简单
	// 通过查询北极星得到被注册的实例，直接删除，更加可靠。
	polarisIPs := p.filterPolarisMetadata(service, instances)

	log.Infof("deRegistered [%s/%s] IPs , FilterPolarisMetadata \n %v \n %v",
		service.GetNamespace(), service.GetName(),
		instances, polarisIPs)

	// 平台接口
	return p.deleteInstances(service, polarisIPs)

}

// processSyncNamespaceAndService 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndService(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.Infof("Begin to sync namespaces and service, %s", serviceMsg)

	// demand 模式，service 不包含 sync 注解时，不需要创建 ns、service 和 alias
	if p.config.PolarisController.SyncMode == util.SyncModeDemand {
		if !util.IsServiceSyncEnable(service) {
			return nil
		}
	}

	createNsResponse, err := polarisapi.CreateNamespaces(service.Namespace)
	if err != nil {
		log.Errorf("Failed create namespaces in processSyncNamespaceAndService %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	createSvcResponse, err := polarisapi.CreateService(service)
	if err != nil {
		log.Errorf("Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		return err
	}

	if service.Annotations[util.PolarisAliasNamespace] != "" &&
		service.Annotations[util.PolarisAliasService] != "" {
		createAliasResponse, err := polarisapi.CreateServiceAlias(service)
		if err != nil {
			log.Errorf("Failed create service alias in processSyncNamespaceAndService %s, err %s, resp %v",
				serviceMsg, err, createAliasResponse)
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed create service alias %s, err %s, resp %v", serviceMsg, err, createAliasResponse)
			return err
		}
	}

	return nil
}

// processSyncInstance 同步实例, 获取Endpoint和北极星数据做同步
func (p *PolarisController) processSyncInstance(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	log.Infof("Begin to sync instance %s", serviceMsg)

	endpoint, err := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
	if err != nil {
		log.Errorf("Get endpoint of service %s error %v, ignore", serviceMsg, err)
		return err
	}

	selector := labels.NewSelector()
	for k, v := range service.Spec.Selector {
		reqiure, _ := labels.NewRequirement(k, selection.Equals, []string{v})
		selector.Add(*reqiure)
	}

	pods, err := p.podLister.Pods(service.GetNamespace()).List(selector)
	if err != nil {
		log.Errorf("Get endpoint of service %s error %v, ignore", serviceMsg, err)
		return err
	}

	/*
		1. 先获取当前service Endpoint中的IP信息，IP:Port:Weight
		2. 获取北极星中注册的Service，经过过滤，获取对应的 IP:Port:Weight
		3. 对比，获取三个数组： 增加，更新，删除
		4. 发起增加，更新，删除操作。
	*/
	instances, err := p.getAllInstance(service)
	if err != nil {
		log.Errorf("Get service instances from polaris of service %s error %v ", serviceMsg, err)
		return err
	}
	ipPortMap := getCustomWeight(service, serviceMsg)
	specIPs := address.GetAddressMapFromEndpoints(service, endpoint, pods, ipPortMap)
	currentIPs := address.GetAddressMapFromPolarisInstance(instances, p.config.PolarisController.ClusterName)
	addIns, deleteIns, updateIns := p.CompareInstance(service, specIPs, currentIPs)

	log.Infof("%s Current polaris instance from sdk is %v", serviceMsg, instances)
	log.Infof("%s Spec endpoint instance is %v", serviceMsg, specIPs)
	log.Infof("%s Current polaris instance is %v", serviceMsg, currentIPs)
	log.Infof("%s addIns %v deleteIns %v updateIns %v", serviceMsg, addIns, deleteIns, updateIns)

	var addInsErr, deleteInsErr, updateInsErr error

	enableRegister := service.GetAnnotations()[util.PolarisEnableRegister]
	enableSync := service.GetAnnotations()[util.PolarisSync]
	// 如果 enableRegister = true,那么自注册，平台不负责注册IP
	if enableRegister != noEnableRegister || enableSync != noEnableRegister {
		// 使用platform 接口
		if addInsErr = p.addInstances(service, addIns); addInsErr != nil {
			log.Errorf("Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
		}

		if updateInsErr = p.updateInstances(service, updateIns); updateInsErr != nil {
			log.Errorf("Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
		}
	}

	if deleteInsErr = p.deleteInstances(service, deleteIns); deleteInsErr != nil {
		log.Errorf("Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
	}

	if addInsErr != nil || deleteInsErr != nil || updateInsErr != nil {
		return fmt.Errorf("failed AddInstances %v DeleteInstances %v UpdateInstances %v", addInsErr, deleteInsErr,
			updateInsErr)
	}

	return nil
}

// processUpdateService 处理更新状态下的场景
func (p *PolarisController) processUpdateService(old, cur *v1.Service) (err error) {
	/*	更新分情况
		1. service 服务参数有变更[customWeight, ttl, autoRegister]
		   * 同步该service
		   * 更新serviceCache缓存
		2. service 无变更，那么可能只有endpoint变化
		   * 仅需要同步对应的endpoint即可
		   * 更新serviceCache缓存
	*/
	if (p.config.PolarisController.SyncMode != util.SyncModeDemand ||
		p.config.PolarisController.SyncMode == util.SyncModeDemand && util.IsServiceSyncEnable(cur)) &&
		util.IfNeedCreateServiceAlias(old, cur) {
		createAliasResponse, err := polarisapi.CreateServiceAlias(cur)
		if err != nil {
			serviceMsg := fmt.Sprintf("[%s,%s]", cur.Namespace, cur.Name)
			log.Errorf("Failed create service alias in processUpdateService %s, err %s, resp %v",
				serviceMsg, err, createAliasResponse)
			p.eventRecorder.Eventf(cur, v1.EventTypeWarning, polarisEvent,
				"Failed create service alias %s, err %s, resp %v", serviceMsg, err, createAliasResponse)
		}
	}

	k8sService := cur.GetNamespace() + "/" + cur.GetName()
	changeType := util.CompareServiceChange(old, cur, p.config.PolarisController.SyncMode)
	switch changeType {
	case util.ServicePolarisDelete:
		log.Infof("Service %s %s, need delete ", k8sService, util.ServicePolarisDelete)
		return p.processDeleteService(old)
	case util.WorkloadKind:
		log.Infof("Service %s changed, need delete old and update", k8sService)
		syncErr := p.processSyncInstance(cur)
		deleteErr := p.processDeleteService(old)
		if syncErr != nil || deleteErr != nil {
			return fmt.Errorf("failed service %s changed, need delete old and update new, %v|%v",
				k8sService, syncErr, deleteErr)
		}
		return
	case util.ServiceMetadataChanged, util.ServiceTTLChanged,
		util.ServiceWeightChanged, util.ServiceCustomWeightChanged:
		log.Infof("Service %s metadata,ttl,custom weight changed, need to update", k8sService)
		return p.processSyncInstance(cur)
	case util.ServiceEnableRegisterChanged:
		log.Infof("Service %s enableRegister,service weight changed, do nothing", k8sService)
		return
	default:
		log.Infof("Service %s endpoints or ports changed", k8sService)
		return p.processSyncInstance(cur)
	}
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
