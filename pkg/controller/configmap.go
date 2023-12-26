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
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/polaris-go/api"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/specification/source/go/api/v1/config_manage"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/wrapperspb"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/cmd/polaris-controller/app/options"
	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

func (p *PolarisController) enqueueConfigMap(task *Task, configmap *v1.ConfigMap, eventType string) {
	metrics.SyncTimes.WithLabelValues(eventType, "ConfigMap").Inc()
	p.insertTask(task)
}

func (p *PolarisController) syncConfigMap(task *Task) error {
	startTime := time.Now()
	defer func() {
		log.SyncConfigScope().Infof("Finished syncing ConfigMap %v. (%v)", task, time.Since(startTime))
	}()

	namespaces := task.Namespace
	name := task.Name
	op := task.Operation

	configMap, err := p.configMapLister.ConfigMaps(namespaces).Get(name)

	switch {
	case errors.IsNotFound(err):
		// 发现对应的 ConfigMap 不存在，即已经被删除了，那么需要从 cache 中查询之前注册的北极星是什么，然后把它从实例中删除
		cachedConfigMap, ok := p.configFileCache.Load(task.Key())
		if !ok {
			// 如果之前的数据也已经不存在那么就跳过不在处理
			log.SyncConfigScope().Infof("ConfigMap %s not in cache. Ignoring the deletion", task.Key())
			return nil
		}
		// 查询原 ConfigMap 是啥，删除对应实例
		processError := p.processDeleteConfigMap(cachedConfigMap)
		if processError != nil {
			// 如果处理有失败的，那就入队列重新处理
			p.eventRecorder.Eventf(cachedConfigMap, v1.EventTypeWarning, polarisEvent, "Delete polaris ConfigMap failed %v",
				processError)
			p.enqueueConfigMap(task, cachedConfigMap, string(op))
			return processError
		}
		p.configFileCache.Delete(task.Key())
	case err != nil:
		log.SyncConfigScope().Errorf("Unable to retrieve ConfigMap %v from store: %v", task.Key(), err)
		p.enqueueConfigMap(task, nil, string(op))
	default:
		// 条件判断将会增加
		// 1. 首次创建 ConfigMap
		// 2. 更新 ConfigMap
		operationConfigMap := configMap

		// 如果是 namespace 流程传来的任务，且 op 为 add ，为 ConfigMap 打上 sync 标签
		if op == OperationAdd && p.IsPolarisConfigMap(operationConfigMap) {
			operationConfigMap = configMap.DeepCopy()
			metav1.SetMetaDataAnnotation(&operationConfigMap.ObjectMeta, util.PolarisSync, util.IsEnableSync)
		}
		if _, ok := operationConfigMap.Annotations[util.PolarisConfigGroup]; !ok {
			groupName := p.config.PolarisController.ClusterName
			if p.config.PolarisController.ConfigSync.DefaultGroup != "" {
				groupName = p.config.PolarisController.ConfigSync.DefaultGroup
			}
			metav1.SetMetaDataAnnotation(&operationConfigMap.ObjectMeta, util.PolarisConfigGroup, groupName)
		}
		// 记录ConfigMap 的 sync 来源 kubernetes 集群
		metav1.SetMetaDataLabel(&operationConfigMap.ObjectMeta, util.InternalConfigFileSyncSourceClusterKey,
			p.config.PolarisController.ClusterName)

		cachedConfigMap, ok := p.configFileCache.Load(task.Key())
		if !ok {
			// 1. cached中没有数据，为首次添加场景
			log.SyncConfigScope().Infof("ConfigMap %s not in cache, begin to add new polaris", task.Key())
			// 同步 k8s 的 namespace 和 configmap 到 polaris，失败投入队列重试
			if processError := p.processSyncNamespaceAndConfigMap(operationConfigMap); processError != nil {
				log.SyncConfigScope().Errorf("ns/config %s sync failed", task.Key())
				return processError
			}
		} else {
			// 2. cached中如果有数据，则是更新操作，需要对比具体是更新了什么，以决定如何处理。
			if processError := p.processUpdateConfigMap(cachedConfigMap, operationConfigMap); processError != nil {
				log.SyncConfigScope().Errorf("ConfigMap %s update failed", task.Key())
				return processError
			}
		}
		p.configFileCache.Store(task.Key(), configMap)
	}
	return nil
}

// processSyncNamespaceAndConfigMap 通过 k8s ns 和 service 到 polaris
// polarisapi.Createxx 中做了兼容，创建时，若资源已经存在，不返回 error
func (p *PolarisController) processSyncNamespaceAndConfigMap(configmap *v1.ConfigMap) error {
	serviceMsg := fmt.Sprintf("[%s/%s]", configmap.GetNamespace(), configmap.GetName())

	// ConfigMap 包含sync注解，但是sync注解为false, 不需要创建对应的 ConfigMap
	if !p.IsPolarisConfigMap(configmap) {
		return nil
	}

	createNsResponse, err := polarisapi.CreateNamespaces(configmap.Namespace)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed create namespaces in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createNsResponse)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create namespaces %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}

	log.SyncConfigScope().Infof("Create ConfigMap %s/%s", configmap.GetNamespace(), configmap.GetName())
	createFileResp, err := polarisapi.CreateConfigMap(configmap)
	if err != nil {
		log.SyncConfigScope().Errorf("Failed create ConfigMap in processSyncNamespaceAndConfigMap %s, err %s, resp %v",
			serviceMsg, err, createFileResp)
		p.eventRecorder.Eventf(configmap, v1.EventTypeWarning, polarisEvent,
			"Failed create ConfigMap %s, err %s, resp %v", serviceMsg, err, createNsResponse)
		return err
	}
	return nil
}

// processUpdateConfigMap 处理更新状态下的场景
func (p *PolarisController) processUpdateConfigMap(old, cur *v1.ConfigMap) error {
	log.SyncConfigScope().Infof("Update ConfigMap %s/%s", cur.GetNamespace(), cur.GetName())
	if !p.IsPolarisConfigMap(cur) {
		log.SyncConfigScope().Infof("user cancel sync ConfigMap %s/%s, delete(%v)", cur.GetNamespace(),
			cur.GetName(), p.allowDeleteConfig())
		return p.processDeleteConfigMap(cur)
	}
	_, err := polarisapi.UpdateConfigMap(cur)
	return err
}

func (p *PolarisController) processDeleteConfigMap(configmap *v1.ConfigMap) error {
	if !p.allowDeleteConfig() {
		return nil
	}
	log.SyncConfigScope().Infof("delete ConfigMap %s/%s", configmap.GetNamespace(), configmap.GetName())

	// 平台接口
	return polarisapi.DeleteConfigMap(configmap)
}

func (p *PolarisController) onConfigMapAdd(cur interface{}) {
	if !p.AllowSyncFromConfigMap() {
		return
	}
	configMap, ok := cur.(*v1.ConfigMap)
	if !ok {
		return
	}

	if !p.IsPolarisConfigMap(configMap) {
		return
	}

	task := &Task{
		Namespace:  configMap.GetNamespace(),
		Name:       configMap.GetName(),
		ObjectType: KubernetesConfigMap,
		Operation:  OperationAdd,
	}

	p.enqueueConfigMap(task, configMap, "Add")
	p.resyncConfigFileCache.Store(task.Key(), configMap)
}

func (p *PolarisController) onConfigMapUpdate(old, cur interface{}) {
	if !p.AllowSyncFromConfigMap() {
		return
	}

	oldCm, ok1 := old.(*v1.ConfigMap)
	curCm, ok2 := cur.(*v1.ConfigMap)

	if !(ok1 && ok2) {
		log.SyncConfigScope().Errorf("Error get update ConfigMaps old %v, new %v", old, cur)
		return
	}

	task := &Task{
		Namespace:  curCm.GetNamespace(),
		Name:       curCm.GetName(),
		ObjectType: KubernetesConfigMap,
	}

	// 这里需要确认是否加入 ConfigMap 进行更新
	// 1. 必须是polaris类型的才需要进行更新
	// 2. 如果: [old/polaris -> new/polaris] 需要更新
	// 3. 如果: [old/polaris -> new/not polaris] 需要更新，相当与删除
	// 4. 如果: [old/not polaris -> new/polaris] 相当于新建
	// 5. 如果: [old/not polaris -> new/not polaris] 舍弃
	oldIsPolaris := p.IsPolarisConfigMap(oldCm)
	curIsPolaris := p.IsPolarisConfigMap(curCm)
	if !oldIsPolaris && !curIsPolaris {
		return
	}

	// 现在已经不是需要同步的北极星配置
	if !curIsPolaris {
		task.Operation = OperationUpdate
		p.enqueueConfigMap(task, oldCm, "Update")
		p.resyncConfigFileCache.Delete(task.Key())
		return
	}

	if oldIsPolaris {
		// 原来就是北极星类型的，增加cache
		task.Operation = OperationUpdate
		p.enqueueConfigMap(task, oldCm, "Update")
		p.configFileCache.Store(task.Key(), oldCm)
	} else if curIsPolaris {
		task.Operation = OperationAdd
		// 原来不是北极星的，新增是北极星的，入队列
		p.enqueueConfigMap(task, curCm, "Add")
	}
	p.resyncConfigFileCache.Store(task.Key(), curCm)
}

func (p *PolarisController) onConfigMapDelete(cur interface{}) {
	if !p.AllowSyncFromConfigMap() {
		return
	}

	configmap, ok := cur.(*v1.ConfigMap)
	if !ok {
		return
	}

	if !p.IsPolarisConfigMap(configmap) {
		return
	}

	task := &Task{
		Namespace:  configmap.GetNamespace(),
		Name:       configmap.GetName(),
		ObjectType: KubernetesConfigMap,
		Operation:  OperationDelete,
	}

	p.enqueueConfigMap(task, configmap, "Delete")
	p.resyncConfigFileCache.Delete(task.Key())
}

func (p *PolarisController) watchPolarisConfig() error {
	conn, err := grpc.Dial(polarisapi.PolarisConfigGrpc, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}

	discoverClient, err := config_manage.NewPolarisConfigGRPCClient(conn).Discover(context.Background())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())

	watcher := &PolarisConfigWatcher{
		controller:     p,
		k8sClient:      p.client,
		config:         p.config,
		conn:           conn,
		configAPI:      api.NewConfigFileAPIBySDKContext(p.consumer.SDKContext()),
		discoverClient: discoverClient,
		namespaces:     util.NewSyncSet[string](),
		needSyncFiles:  util.NewSyncMap[string, *util.SyncMap[string, *util.SyncMap[string, *configFileRefrence]]](),
		filesRevisions: util.NewSyncMap[string, string](),
		groups:         util.NewSyncMap[string, *util.SyncSet[string]](),
		groupRevisions: util.NewSyncMap[string, string](),
		cancel:         cancel,
	}

	p.polarisConfigWatcher = watcher
	return watcher.Start(ctx)
}

type configFileRefrence struct {
	Revision uint64
	Data     *config_manage.ClientConfigFileInfo
}

type PolarisConfigWatcher struct {
	//
	controller *PolarisController
	// config provides access to init options for a given controller
	config options.KubeControllerManagerConfiguration
	// nsWatcher
	nsWatcher watch.Interface
	// k8sClient
	k8sClient clientset.Interface
	// configAPI
	configAPI api.ConfigFileAPI
	// conn
	conn *grpc.ClientConn
	// discoverClient
	discoverClient config_manage.PolarisConfigGRPC_DiscoverClient
	// namespaces
	namespaces *util.SyncSet[string]
	// needSyncFiles 需要同步到 k8s ConfigMap 的配置文件
	needSyncFiles *util.SyncMap[string, *util.SyncMap[string, *util.SyncMap[string, *configFileRefrence]]]
	// filesRevisions 文件列表版本
	filesRevisions *util.SyncMap[string, string]
	// groups
	groups *util.SyncMap[string, *util.SyncSet[string]]
	// groupRevisions
	groupRevisions *util.SyncMap[string, string]
	//
	cancel context.CancelFunc
	//
	executor *util.TaskExecutor
}

func (p *PolarisConfigWatcher) Start(ctx context.Context) error {
	p.executor = util.NewExecutor(runtime.NumCPU())
	nsList, err := p.k8sClient.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range nsList.Items {
		p.namespaces.Add(nsList.Items[i].Name)
	}
	watcher, err := p.k8sClient.CoreV1().Namespaces().Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	p.nsWatcher = watcher

	go p.watchNamespaces(ctx)
	go p.fetchResources(ctx)
	go p.doRecv(ctx)

	return nil
}

func (p *PolarisConfigWatcher) Destroy() error {
	p.cancel()
	p.nsWatcher.Stop()
	_ = p.conn.Close()
	return nil
}

func (p *PolarisConfigWatcher) doRecv(ctx context.Context) {
	discoverClient := p.discoverClient
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := discoverClient.Recv()
			if err != nil {
				log.SyncConfigMapScope().Info("receive fetch config resource fail", zap.Error(err))
				continue
			}

			log.SyncConfigMapScope().Infof("receive fetch config resource for type(%v) code(%d)",
				msg.GetType().String(), msg.GetCode())
			if msg.Code != uint32(apimodel.Code_ExecuteSuccess) {
				continue
			}

			switch msg.Type {
			case config_manage.ConfigDiscoverResponse_CONFIG_FILE_GROUPS:
				p.receiveGroups(msg)
			case config_manage.ConfigDiscoverResponse_CONFIG_FILE_Names:
				p.receiveConfigFiles(msg)
			case config_manage.ConfigDiscoverResponse_CONFIG_FILE:
				//
			}
		}
	}
}

func (p *PolarisConfigWatcher) watchNamespaces(ctx context.Context) {
	for {
		select {
		case e := <-p.nsWatcher.ResultChan():
			if e.Object == nil {
				continue
			}
			event := e.Object.(*v1.Namespace)
			switch e.Type {
			case watch.Added, watch.Modified:
				p.namespaces.Add(event.Name)
			case watch.Deleted:
				p.namespaces.Remove(event.Name)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *PolarisConfigWatcher) fetchResources(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	for {
		select {
		case <-ticker.C:
			p.namespaces.Range(func(nsName string) {
				p.fetchGroups(nsName)
			})
		case <-ctx.Done():
			return
		}
	}
}

func (p *PolarisConfigWatcher) fetchGroups(ns string) {
	log.SyncConfigMapScope().Debugf("begin fetch config groups for namespace(%v)", ns)
	p.groups.ComputeIfAbsent(ns, func(k string) *util.SyncSet[string] {
		return util.NewSyncSet[string]()
	})
	p.needSyncFiles.ComputeIfAbsent(ns, func(k string) *util.SyncMap[string, *util.SyncMap[string, *configFileRefrence]] {
		return util.NewSyncMap[string, *util.SyncMap[string, *configFileRefrence]]()
	})

	preRevision, _ := p.groupRevisions.Load(ns)
	discoverClient := p.discoverClient
	if err := discoverClient.Send(&config_manage.ConfigDiscoverRequest{
		Type: config_manage.ConfigDiscoverRequest_CONFIG_FILE_GROUPS,
		ConfigFile: &config_manage.ClientConfigFileInfo{
			Namespace: wrapperspb.String(ns),
		},
		Revision: preRevision,
	}); err != nil {
		log.SyncConfigMapScope().Errorf("fetch config groups failed, %v", err)
	}
}

func (p *PolarisConfigWatcher) receiveGroups(resp *config_manage.ConfigDiscoverResponse) {
	groups := resp.ConfigFileGroups
	if len(groups) == 0 {
		return
	}

	p.groupRevisions.Store(resp.GetConfigFile().GetNamespace().GetValue(), resp.GetRevision())
	for i := range groups {
		item := groups[i]
		nsName := item.GetNamespace().GetValue()
		groupName := item.GetName().GetValue()
		groups, _ := p.groups.Load(nsName)
		groups.Add(groupName)

		nsBucket, _ := p.needSyncFiles.Load(nsName)
		nsBucket.ComputeIfAbsent(groupName, func(k string) *util.SyncMap[string, *configFileRefrence] {
			return util.NewSyncMap[string, *configFileRefrence]()
		})
	}
}

func (p *PolarisConfigWatcher) fetchConfigFiles(namespace, group string) {
	log.SyncConfigMapScope().Debugf("begin fetch config files for namespace(%v) group(%s)", namespace, group)
	key := namespace + "/" + group
	preRevision, _ := p.filesRevisions.Load(key)
	discoverClient := p.discoverClient
	// 只要 namespace/group 下任意一个文件发生变动，revision 都会变化，因此不需要在单独监听每个配置文件
	if err := discoverClient.Send(&config_manage.ConfigDiscoverRequest{
		Type: config_manage.ConfigDiscoverRequest_CONFIG_FILE_Names,
		ConfigFile: &config_manage.ClientConfigFileInfo{
			Namespace: wrapperspb.String(namespace),
			Group:     wrapperspb.String(group),
		},
		Revision: preRevision,
	}); err != nil {
		log.SyncConfigMapScope().Errorf("fetch config files failed, %v", err)
	}
}

func (p *PolarisConfigWatcher) receiveConfigFiles(resp *config_manage.ConfigDiscoverResponse) {
	files := resp.ConfigFileNames
	if len(files) == 0 {
		return
	}

	var (
		start   = time.Now()
		wait    = &sync.WaitGroup{}
		syncCnt = &atomic.Int32{}
		delCnt  = &atomic.Int32{}
	)

	for i := range files {
		item := files[i]
		nsName := item.GetNamespace().GetValue()
		groupName := item.GetGroup().Value
		fileName := item.GetFileName().GetValue()
		nsBucket, _ := p.needSyncFiles.Load(nsName)
		groupBucket, _ := nsBucket.Load(groupName)
		if p.allowSyncToConfigMap(item) {
			val, isNew := groupBucket.ComputeIfAbsent(fileName, func(k string) *configFileRefrence {
				return &configFileRefrence{
					Revision: 0,
				}
			})
			if isNew || val.Revision <= item.GetVersion().GetValue() {
				val.Revision = item.GetVersion().Value
				groupBucket.Store(fileName, val)
				wait.Add(1)
				log.SyncConfigMapScope().Info("begin fetch config file", zap.String("namespace", nsName),
					zap.String("group", groupName), zap.String("file", fileName), zap.Uint64("cur-version", val.Revision))
				// 异步任务进行任务处理
				p.executor.Execute(func() {
					defer wait.Done()
					p.fetchConfigFile(val.Data)
					syncCnt.Add(1)
				})
			}
		} else {
			delCnt.Add(1)
			groupBucket.Delete(fileName)
			err := p.k8sClient.CoreV1().ConfigMaps(nsName).Delete(context.Background(),
				fmt.Sprintf("%s/%s", groupName, fileName), metav1.DeleteOptions{})
			if err != nil {
				log.SyncConfigMapScope().Errorf("delete config map failed, %v", err)
			}
		}
	}

	wait.Wait()
	log.SyncConfigMapScope().Debug("finish config map sync", zap.Duration("cost", time.Since(start)),
		zap.Int32("add", syncCnt.Load()), zap.Int32("del", delCnt.Load()))
}

func (p *PolarisConfigWatcher) fetchConfigFile(req *config_manage.ClientConfigFileInfo) {

	file, err := p.configAPI.FetchConfigFile(&api.GetConfigFileRequest{
		GetConfigFileRequest: &model.GetConfigFileRequest{
			Namespace: req.GetNamespace().GetValue(),
			FileGroup: req.GetGroup().GetValue(),
			FileName:  req.GetFileName().GetValue(),
		},
	})
	if err != nil {
		log.SyncConfigMapScope().Errorf("fetch config file failed, %v", err)
		return
	}
	log.SyncConfigMapScope().Info("finish fetch config file", zap.String("namespace", file.GetNamespace()),
		zap.String("group", file.GetFileGroup()), zap.String("file", file.GetFileName()))
	namespace := file.GetNamespace()

	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s/%s", file.GetFileGroup(), file.GetFileName()),
			Namespace: namespace,
			Labels:    file.GetLabels(),
		},
		Data: map[string]string{
			file.GetFileName(): file.GetContent(),
		},
	}

	_, err = p.k8sClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), cm, metav1.CreateOptions{})
	if err == nil {
		return
	}
	if errors.IsAlreadyExists(err) {
		_, err = p.k8sClient.CoreV1().ConfigMaps(namespace).Update(context.Background(), cm, metav1.UpdateOptions{})
	}
	if err != nil {
		log.SyncConfigMapScope().Errorf("update config file failed, %v", err)
	}
}

const (
	MetaKeyConfigFileSyncToKubernetes = "internal-sync-to-kubernetes"
)

func (p *PolarisConfigWatcher) allowSyncToConfigMap(file *config_manage.ClientConfigFileInfo) bool {
	var (
		allowSync       bool
		isSourceFromK8s bool
		isCycleSync     bool
	)

	tags := file.GetTags()
	for i := range tags {
		if tags[i].GetKey().GetValue() == MetaKeyConfigFileSyncToKubernetes {
			if tags[i].GetValue().GetValue() == "true" {
				allowSync = true
			}
		}
		if tags[i].GetKey().GetValue() == util.InternalConfigFileSyncSourceKey {
			if tags[i].GetValue().GetValue() == util.SourceFromKubernetes {
				isSourceFromK8s = true
			}
		}
		if tags[i].GetKey().GetValue() == util.InternalConfigFileSyncSourceClusterKey {
			if tags[i].GetValue().GetValue() == p.config.PolarisController.ClusterName {
				isCycleSync = true
			}
		}
	}
	return allowSync && (!isSourceFromK8s && !isCycleSync)
}

func ToTagMap(file *config_manage.ClientConfigFileInfo) map[string]string {
	tags := file.GetTags()
	kvs := map[string]string{
		"version": strconv.Itoa(int(file.GetVersion().GetValue())),
	}
	for i := range tags {
		kvs[tags[i].GetKey().GetValue()] = tags[i].GetValue().GetValue()
	}

	return kvs
}
