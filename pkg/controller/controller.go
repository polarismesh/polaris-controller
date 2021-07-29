package controller

import (
	"fmt"
	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	localCache "github.com/polarismesh/polaris-controller/pkg/cache"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	"k8s.io/klog"
	"reflect"
	"strings"
	"time"
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
	isAutoRegister              = "true"
	polarisEvent                = "PolarisRegister"
)

// NewPolarisController
func NewPolarisController(podInformer coreinformers.PodInformer,
	serviceInformer coreinformers.ServiceInformer,
	endpointsInformer coreinformers.EndpointsInformer,
	namespaceInformer coreinformers.NamespaceInformer,
	client clientset.Interface,
	config options.KubeControllerManagerConfiguration) *PolarisController {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(klog.Infof)
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
		AddFunc: p.onNamespaceAdd,
	})

	p.serviceLister = serviceInformer.Lister()
	p.servicesSynced = serviceInformer.Informer().HasSynced

	p.podLister = podInformer.Lister()
	p.podsSynced = podInformer.Informer().HasSynced

	p.endpointsLister = endpointsInformer.Lister()
	p.endpointsSynced = endpointsInformer.Informer().HasSynced

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

	defer klog.Infof("Shutting down polaris controller")

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

	key, err := util.KeyFunc(curService)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", curService, err))
		return
	}

	if !(ok1 && ok2) {
		klog.Errorf("Error get update services old %v, new %v", old, current)
		return
	}

	// 为什么不适用这个做对比？ 因为可以利用re sync做同步对账，如果对比version，还要自在写同步逻辑
	//if ok1 && ok2 && oldService.GetResourceVersion() == curService.GetResourceVersion() {
	//	return
	//}

	klog.V(6).Infof("Service %s/%s is update", curService.GetNamespace(), curService.GetName())
	// 这里需要确认是否加入svc进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := util.IsPolarisService(oldService)
	curIsPolaris := util.IsPolarisService(curService)
	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		klog.Infof("Service %s is update polaris config", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
		p.serviceCache.Store(key, oldService)
	} else if curIsPolaris {
		// 原来不是北极星的，新增是北极星的，入队列
		klog.Infof("Service %s is update to polaris type", key)
		metrics.SyncTimes.WithLabelValues("Update", "Service").Inc()
		p.queue.Add(key)
	}
}

// onServiceAdd 在识别到是这个北极星类型service的时候进行处理
func (p *PolarisController) onServiceAdd(obj interface{}) {
	key, err := util.KeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	service := obj.(*v1.Service)
	p.enqueueService(service, key, "Add")

}

// onServiceDelete 在识别到是这个北极星类型service删除的时候，才进行处理
// 判断这个是否是北极星service，如果是北极星service，就加入队列。
func (p *PolarisController) onServiceDelete(obj interface{}) {
	key, err := util.KeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	svc := obj.(*v1.Service)
	p.enqueueService(svc, key, "Delete")

}

func (p *PolarisController) onEndpointAdd(obj interface{}) {

	key, err := util.KeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	// 监听到创建service，处理是通常endpoint还没有创建，导致这时候读取endpoint 为not found。
	endpoint := obj.(*v1.Endpoints)
	p.enqueueEndpoint(key, endpoint, "Add")
}

func (p *PolarisController) onNamespaceAdd(obj interface{}) {
	key, err := util.KeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	namespace := obj.(*v1.Namespace)
	p.enqueueNamespace(key, namespace)
}

func (p *PolarisController) onEndpointUpdate(old, cur interface{}) {
	// 先确认service是否是Polaris的，后再做比较，会提高效率。
	key, err := util.KeyFunc(cur)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", cur, err))
		return
	}

	oldEndpoint, ok1 := old.(*v1.Endpoints)
	curEndpoint, ok2 := cur.(*v1.Endpoints)

	if !util.IgnoreEndpoint(curEndpoint) {
		return
	}

	service, err := p.serviceLister.Services(curEndpoint.GetNamespace()).Get(curEndpoint.GetName())
	if err != nil {
		klog.V(6).Infof("Unable to find the service of the endpoint %s, %v", key, err)
		return
	}

	if util.IsPolarisService(service) {
		// 这里有不严谨的情况， endpoint update 时的service有从
		// 1. polaris -> not polaris
		// 2. not polaris -> polaris
		// 3. 只变更 endpoint.subsets
		if ok1 && ok2 && !reflect.DeepEqual(oldEndpoint.Subsets, curEndpoint.Subsets) {
			metrics.SyncTimes.WithLabelValues("Update", "Endpoint").Inc()
			klog.Infof("Endpoints %s is updating, in queue", key)
			p.queue.Add(key)
		}
	}

}

func (p *PolarisController) onEndpointDelete(obj interface{}) {
	key, err := util.KeyFunc(obj)
	if err != nil {
		runtime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	// 考虑删除service的时候，此时同上endpoint还完全剔除。当endpoint delete监听到时，一般已经完成剔除逻辑
	endpoint := obj.(*v1.Endpoints)
	p.enqueueEndpoint(key, endpoint, "Delete")

}

func (p *PolarisController) enqueueNamespace(key string, namespace *v1.Namespace) {

	if !util.IgnoreNamespace(namespace) {
		klog.V(6).Infof("%s in ignore namespaces", key)
		return
	}
	p.queue.Add(key)
}

func (p *PolarisController) enqueueEndpoint(key string, endpoint *v1.Endpoints, eventType string) {

	if !util.IgnoreEndpoint(endpoint) {
		klog.V(6).Infof("Endpoint %s in ignore namespaces", key)
		return
	}

	service, err := p.serviceLister.Services(endpoint.GetNamespace()).Get(endpoint.GetName())
	if err != nil {
		klog.V(6).Infof("Unable to find the service of the endpoint %s, %v", key, err)
		return
	}
	if util.IsPolarisService(service) {
		klog.Infof("Endpoint %s is polaris, in queue", key)
		metrics.SyncTimes.WithLabelValues(eventType, "Endpoint").Inc()
		p.queue.Add(key)
	}
}

func (p *PolarisController) enqueueService(service *v1.Service, key string, eventType string) {

	if eventType == "Add" {
		// 如果是添加事件
		if !util.IgnoreService(service) {
			return
		}
	} else {
		if !util.IsPolarisService(service) {
			return
		}
	}
	klog.Infof("Service %s is polaris type, in queue", key)
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
		klog.Errorf("Error syncing service %q, retrying. Error: %v", key, err)
		p.queue.AddRateLimited(key)
		return
	}

	klog.Warningf("Dropping service %q out of the queue: %v", key, err)
	p.queue.Forget(key)
	runtime.HandleError(err)
}

func (p *PolarisController) syncNamespace(key string) error {

	klog.Infof("Begin to sync namespaces %s", key)

	createNsResponse, err := polarisapi.CreateNamespaces(key)
	if err != nil {
		klog.Errorf("Failed create namespaces in syncNamespace %s, err %s, resp %v",
			key, err, createNsResponse)
		return err
	}

	return nil
}

func (p *PolarisController) syncService(key string) error {
	klog.Infof("Start sync service %s, queue deep %d", key, p.queue.Len())
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing service %q service. (%v)", key, time.Since(startTime))
	}()
	namespaces, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.Errorf("SplitMetaNamespaceKey %s failed, %v", key, err)
		return err
	}

	service, err := p.serviceLister.Services(namespaces).Get(name)

	switch {
	case errors.IsNotFound(err):
		// 发现对应的service不存在，即已经被删除了，那么需要从cache中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedService, ok := p.serviceCache.Load(key)
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			klog.Errorf("Service %s not in cache even though the watcher thought it was. Ignoring the deletion", key)
			return nil
		}
		klog.Infof("Service %s is in cache, cache info %v", key, cachedService.Name)
		// 查询原svc是啥，删除对应实例
		processError := p.processDeleteService(cachedService)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedService, v1.EventTypeWarning, polarisEvent, "Delete polaris instances failed %v",
				processError)
			return processError
		}
		p.serviceCache.Delete(key)
	case err != nil:
		klog.Errorf("Unable to retrieve service %v from store: %v", key, err)
	default:
		// 条件判断将会增加
		// 1. 首次创建service
		// 2. 更新service
		//    a. 北极星相关信息不变，更新了对应的metadata,ttl,权重信息
		//    b. 北极星相关信息改变，相当于删除旧的北极星信息，注册新的北极星信息。
		klog.Infof("Begin to process service %s", key)
		cachedService, ok := p.serviceCache.Load(key)
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			klog.Infof("Service %s not in cache, begin to add new polaris", key)

			// 同步 k8s 的 namespace 和 service 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndService(service); processError != nil {
				klog.Errorf("ns/svc %s sync failed", key)
				return processError
			}

			if !util.IsPolarisService(service) {
				// 不合法的 service ，只把 service 同步到北极星，不做后续的实例处理
				klog.Info("service is not valid, do not process ", key)
				return nil
			}

			if processError := p.processSyncInstance(service); processError != nil {
				klog.Errorf("Service %s first add failed", key)
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateService(cachedService, service); processError != nil {
				klog.Errorf("Service %s update add failed", key)
				return processError
			}
		}
		p.serviceCache.Store(key, service)
	}
	return nil
}

func (p *PolarisController) processDeleteService(service *v1.Service) (err error) {
	if util.IsPolarisService(service) {

		klog.Infof("Delete Service %s/%s", service.GetNamespace(), service.GetName())

		instances, err := p.getAllInstance(service)
		if err != nil {
			return err
		}
		// 筛选出来只属于这个service的IP
		// 更简单的做法，直接从cachedEndpoint删除，处理简单
		// 通过查询北极星得到被注册的实例，直接删除，更加可靠。
		polarisIPs := p.filterPolarisMetadata(service, instances)

		klog.V(6).Infof("deRegistered [%s/%s] IPs , FilterPolarisMetadata \n %v \n %v",
			service.GetNamespace(), service.GetName(),
			instances, polarisIPs)

		// 平台接口
		return p.deleteInstances(service, polarisIPs)
	}
	return nil
}

// processSyncNamespaceAndService 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndService(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	klog.Infof("Begin to sync namespaces and service, %s", serviceMsg)

	createNsResponse, err := polarisapi.CreateNamespaces(service.Namespace)
	if err != nil {
		klog.Errorf("Failed create namespaces in processSyncNamespaceAndService %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	createSvcResponse, err := polarisapi.CreateService(service)
	if err != nil {
		klog.Errorf("Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed create service %s, err %s", serviceMsg, createSvcResponse.Info)
		return err
	}
	return nil
}

// processSyncInstance 同步实例, 获取Endpoint和北极星数据做同步
func (p *PolarisController) processSyncInstance(service *v1.Service) (err error) {
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	klog.Infof("Begin to sync instance %s", serviceMsg)

	endpoint, err := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
	if err != nil {
		klog.Errorf("Get endpoint of service %s error %v, ignore", serviceMsg, err)
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
		klog.Errorf("Get service instances from polaris of service %s error %v ", serviceMsg, err)
		return err
	}
	ipPortMap := getCustomWeight(service, serviceMsg)
	specIPs := address.GetAddressMapFromEndpoints(service, endpoint, ipPortMap)
	currentIPs := address.GetAddressMapFromPolarisInstance(instances, p.config.PolarisController.ClusterName)
	addIns, deleteIns, updateIns := p.CompareInstance(service, specIPs, currentIPs)

	klog.Infof("%s Current polaris instance from sdk is %v", serviceMsg, instances)
	klog.Infof("%s Spec endpoint instance is %v", serviceMsg, specIPs)
	klog.Infof("%s Current polaris instance is %v", serviceMsg, currentIPs)
	klog.Infof("%s addIns %v deleteIns %v updateIns %v", serviceMsg, addIns, deleteIns, updateIns)

	var addInsErr, deleteInsErr, updateInsErr error

	autoRegister := service.GetAnnotations()[util.PolarisAutoRegister]
	// 如果autoRegister = true,那么自注册，平台不负责注册IP
	if autoRegister != isAutoRegister {
		// 使用platform 接口
		if addInsErr = p.addInstances(service, addIns); addInsErr != nil {
			klog.Errorf("Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed AddInstances %s, err %s", serviceMsg, addInsErr.Error())
		}

		if updateInsErr = p.updateInstances(service, updateIns); updateInsErr != nil {
			klog.Errorf("Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
			p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
				"Failed UpdateInstances %s, err %s", serviceMsg, updateInsErr.Error())
		}
	}

	if deleteInsErr = p.deleteInstances(service, deleteIns); deleteInsErr != nil {
		klog.Errorf("Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
		p.eventRecorder.Eventf(service, v1.EventTypeWarning, polarisEvent,
			"Failed DeleteInstances %s, err %s", serviceMsg, deleteInsErr.Error())
	}

	if addInsErr != nil || deleteInsErr != nil || updateInsErr != nil {
		return fmt.Errorf("Failed AddInstances %v DeleteInstances %v UpdateInstances %v", addInsErr, deleteInsErr,
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
	k8sService := cur.GetNamespace() + "/" + cur.GetName()
	changeType := util.CompareServiceChange(old, cur)
	switch changeType {
	case util.ServicePolarisDelete:
		klog.Infof("Service %s %s, need delete ", k8sService, util.ServicePolarisDelete)
		return p.processDeleteService(old)
	case util.WorkloadKind:
		klog.Infof("Service %s changed, need delete old and update", k8sService)
		syncErr := p.processSyncInstance(cur)
		deleteErr := p.processDeleteService(old)
		if syncErr != nil || deleteErr != nil {
			return fmt.Errorf("failed service %s changed, need delete old and update new, %v|%v",
				k8sService, syncErr, deleteErr)
		}
		return
	case util.ServiceMetadataChanged, util.ServiceTTLChanged,
		util.ServiceWeightChanged, util.ServiceCustomWeightChanged:
		klog.Infof("Service %s metadata,ttl,custom weight changed, need to update", k8sService)
		return p.processSyncInstance(cur)
	case util.ServiceAutoRegisterChanged:
		klog.Infof("Service %s autoRegister,service weight changed, do nothing", k8sService)
		return
	default:
		klog.Infof("Service %s endpoints or ports changed", k8sService)
		return p.processSyncInstance(cur)
	}
}

// CounterPolarisService
func (p *PolarisController) CounterPolarisService() {
	serviceList, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("Failed to get service list %v", serviceList)
		return
	}
	metrics.PolarisCount.Reset()
	for _, service := range serviceList {
		if util.IsPolarisService(service) {
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
