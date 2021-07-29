package accountingcontroller

import (
	"time"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	localCache "github.com/polarismesh/polaris-controller/pkg/cache"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/metrics/prometheus/ratelimiter"
	"k8s.io/klog"
)

// PolarisAccountController
type PolarisAccountController struct {
	client           clientset.Interface

	// serviceLister is able to list/get services and is populated by the shared informer passed to
	// NewEndpointController.
	serviceLister corelisters.ServiceLister
	// servicesSynced returns true if the service shared informer has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	servicesSynced cache.InformerSynced

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

	// ComponentConfig provides access to init options for a given controller
	config options.KubeControllerManagerConfiguration
}

const (
	polarisControllerName       = "polaris-accounting-controller"
	maxRetries                  = 10
	metricPolarisControllerName = "polaris_controller"
)

// NewPolarisAccountingController
func NewPolarisAccountingController(serviceInformer coreinformers.ServiceInformer,
	client clientset.Interface,
	config options.KubeControllerManagerConfiguration) *PolarisAccountController {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(klog.Infof)
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: client.CoreV1().Events("")})

	if client != nil && client.CoreV1().RESTClient().GetRateLimiter() != nil {
		_ = ratelimiter.RegisterMetricAndTrackRateLimiterUsage(
			metricPolarisControllerName, client.CoreV1().RESTClient().GetRateLimiter())
	}

	metrics.RegisterMetrics()

	p := &PolarisAccountController{
		client: client,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(),
			polarisControllerName),
		workerLoopPeriod: time.Second,
	}

	p.serviceLister = serviceInformer.Lister()
	p.servicesSynced = serviceInformer.Informer().HasSynced

	p.serviceCache = localCache.NewCachedServiceMap()

	p.config = config
	return p
}

// Run will not return until stopCh is closed. workers determines how many
// endpoints will be handled in parallel.
func (p *PolarisAccountController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	defer klog.Infof("Shutting down polaris controller")

	if !cache.WaitForCacheSync(stopCh, p.servicesSynced) {
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(p.worker, p.workerLoopPeriod, stopCh)
	}

	//定时任务
	go p.AccountingTracker(stopCh)

	<-stopCh

}

func (p *PolarisAccountController) worker() {
	for p.processNextWorkItem() {

	}
}

func (p *PolarisAccountController) processNextWorkItem() bool {
	key, quit := p.queue.Get()
	if quit {
		return false
	}
	defer p.queue.Done(key)

	err := p.syncService(key.(string))

	p.handleErr(err, key)

	return true
}

func (p *PolarisAccountController) handleErr(err error, key interface{}) {
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

func (p *PolarisAccountController) syncService(key string) error {
	return nil
}

// AccountingTracker
func (p *PolarisAccountController) AccountingTracker(stopCh <-chan struct{}) {
	ticker := time.NewTicker(p.config.PolarisController.MinAccountingPeriod.Duration)
	for range ticker.C {
		p.syncPolarisList()
	}
	<-stopCh
}

// syncPolarisList 将北极星服务中，属于tke的，且在本集群注册过的，同步一下
func (p *PolarisAccountController) syncPolarisList() {
	klog.Errorf("start syncing %v", time.Now())
}
