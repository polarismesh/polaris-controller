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
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"sort"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

const (
	syncStateActive        = "active"
	syncStatePendingDelete = "pending-delete"

	syncStateShardCount = 16
	syncStateDataKey    = "records"
	syncStateNamePrefix = "polaris-controller-sync-state-"
	syncStateMaxSize    = 1024 * 1024
	syncStateLabelKey   = "polarismesh.cn/sync-state"
	syncStateLabelValue = "true"
)

type serviceSyncState struct {
	ClusterName      string `json:"clusterName"`
	SourceNamespace  string `json:"sourceNamespace"`
	SourceName       string `json:"sourceName"`
	SourceUID        string `json:"sourceUID"`
	PolarisNamespace string `json:"polarisNamespace"`
	PolarisService   string `json:"polarisService"`
	Phase            string `json:"phase"`
	Attempts         int    `json:"attempts,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	UpdatedAt        string `json:"updatedAt"`
}

func (s serviceSyncState) key() string {
	uid := s.SourceUID
	if uid == "" {
		uid = s.SourceNamespace + "/" + s.SourceName
	}
	return s.ClusterName + "|" + uid
}

func (s serviceSyncState) sourceTask() *Task {
	return &Task{
		Namespace:  s.SourceNamespace,
		Name:       s.key(),
		ObjectType: PolarisSyncState,
		Operation:  OperationDelete,
	}
}

func (s serviceSyncState) service() *v1.Service {
	return &v1.Service{ObjectMeta: metav1.ObjectMeta{
		Namespace: s.SourceNamespace,
		Name:      s.SourceName,
		UID:       types.UID(s.SourceUID),
	}}
}

type syncStateStore struct {
	client      kubernetes.Interface
	namespace   string
	mu          sync.Mutex
	initialized bool
	records     map[string]serviceSyncState
}

func newSyncStateStore(client kubernetes.Interface) *syncStateStore {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = util.RootNamespace
	}
	return &syncStateStore{
		client:    client,
		namespace: namespace,
		records:   make(map[string]serviceSyncState),
	}
}

func newServiceSyncState(service *v1.Service, clusterName, phase string) serviceSyncState {
	return serviceSyncState{
		ClusterName:      clusterName,
		SourceNamespace:  service.GetNamespace(),
		SourceName:       service.GetName(),
		SourceUID:        string(service.GetUID()),
		PolarisNamespace: service.GetNamespace(),
		PolarisService:   service.GetName(),
		Phase:            phase,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

func (p *PolarisController) ensureActiveSyncState(service *v1.Service) error {
	if p.syncStateStore == nil {
		return nil
	}
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStateActive)
	return p.syncStateStore.Upsert(context.Background(), record)
}

func (p *PolarisController) markPendingSyncState(service *v1.Service) error {
	if p.syncStateStore == nil {
		return nil
	}
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStatePendingDelete)
	return p.syncStateStore.Upsert(context.Background(), record)
}

func (p *PolarisController) deleteServiceWithState(service *v1.Service) error {
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStatePendingDelete)
	if p.syncStateStore != nil {
		if err := p.syncStateStore.Upsert(context.Background(), record); err != nil {
			return fmt.Errorf("persist pending deletion for %s/%s: %w",
				service.GetNamespace(), service.GetName(), err)
		}
	}

	if err := p.processDeleteService(service); err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	if p.syncStateStore != nil {
		if err := p.syncStateStore.Remove(context.Background(), record); err != nil {
			return fmt.Errorf("remove sync state for %s/%s: %w",
				service.GetNamespace(), service.GetName(), err)
		}
	}
	return nil
}

func (p *PolarisController) processPendingSyncState(task *Task) error {
	if p.syncStateStore == nil {
		return nil
	}
	record, ok, err := p.syncStateStore.Get(context.Background(), task.Name)
	if err != nil {
		return err
	}
	if !ok || record.Phase != syncStatePendingDelete {
		return nil
	}
	if record.ClusterName != p.config.PolarisController.ClusterName {
		log.SyncNamingScope().Warnf("skip pending sync state %s owned by cluster %s",
			record.key(), record.ClusterName)
		return nil
	}

	service := record.service()
	if err = p.processDeleteService(service); err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	return p.syncStateStore.Remove(context.Background(), record)
}

func (p *PolarisController) enqueuePendingSyncStatesForService(namespace, name string) (int, error) {
	if p.syncStateStore == nil {
		return 0, nil
	}
	records, err := p.syncStateStore.MarkPendingForSource(context.Background(),
		p.config.PolarisController.ClusterName, namespace, name)
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		p.insertTask(record.sourceTask())
	}
	return len(records), nil
}

func (p *PolarisController) recordSyncStateFailure(record serviceSyncState, failure error) {
	if p.syncStateStore == nil {
		return
	}
	record.Attempts++
	record.LastError = failure.Error()
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := p.syncStateStore.Upsert(context.Background(), record); err != nil {
		log.SyncNamingScope().Errorf("failed to persist sync state error for %s: %v", record.key(), err)
	}
}

func (s *syncStateStore) Upsert(ctx context.Context, record serviceSyncState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initializeLocked(ctx); err != nil {
		return err
	}
	if existing, ok := s.records[record.key()]; ok &&
		existing.Phase == record.Phase &&
		existing.PolarisNamespace == record.PolarisNamespace &&
		existing.PolarisService == record.PolarisService &&
		existing.LastError == record.LastError &&
		existing.Attempts == record.Attempts {
		return nil
	}
	if err := s.writeRecordLocked(ctx, record); err != nil {
		return err
	}
	s.records[record.key()] = record
	return nil
}

func (s *syncStateStore) Remove(ctx context.Context, record serviceSyncState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initializeLocked(ctx); err != nil {
		return err
	}
	if _, ok := s.records[record.key()]; !ok {
		return nil
	}
	if err := s.deleteRecordLocked(ctx, record); err != nil {
		return err
	}
	delete(s.records, record.key())
	return nil
}

func (s *syncStateStore) Get(ctx context.Context, key string) (serviceSyncState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initializeLocked(ctx); err != nil {
		return serviceSyncState{}, false, err
	}
	record, ok := s.records[key]
	return record, ok, nil
}

func (s *syncStateStore) List(ctx context.Context) ([]serviceSyncState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initializeLocked(ctx); err != nil {
		return nil, err
	}
	records := make([]serviceSyncState, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].key() < records[j].key()
	})
	return records, nil
}

func (s *syncStateStore) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
	s.records = make(map[string]serviceSyncState)
	return s.initializeLocked(ctx)
}

func (s *syncStateStore) MarkPendingForSource(
	ctx context.Context, clusterName, namespace, name string,
) ([]serviceSyncState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.initializeLocked(ctx); err != nil {
		return nil, err
	}

	var pending []serviceSyncState
	for key, record := range s.records {
		if record.ClusterName != clusterName ||
			record.SourceNamespace != namespace || record.SourceName != name {
			continue
		}
		if record.Phase != syncStatePendingDelete {
			record.Phase = syncStatePendingDelete
			record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err := s.writeRecordLocked(ctx, record); err != nil {
				return nil, err
			}
			s.records[key] = record
		}
		pending = append(pending, record)
	}
	return pending, nil
}

func (s *syncStateStore) initializeLocked(ctx context.Context) error {
	if s.initialized {
		return nil
	}
	if s.client == nil {
		return fmt.Errorf("kubernetes client is not configured")
	}
	s.records = make(map[string]serviceSyncState)

	for shard := 0; shard < syncStateShardCount; shard++ {
		name := syncStateConfigMapName(shard)
		configMap, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			configMap = &v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: s.namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": polarisControllerName,
						syncStateLabelKey:              syncStateLabelValue,
					},
					Annotations: map[string]string{
						"polarismesh.cn/schema-version": "1",
					},
				},
				Data: map[string]string{syncStateDataKey: "{}"},
			}
			if _, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					return err
				}
				configMap, err = s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}

		shardRecords, err := decodeSyncStateRecords(configMap)
		if err != nil {
			return fmt.Errorf("decode sync state ConfigMap %s/%s: %w", s.namespace, name, err)
		}
		for key, record := range shardRecords {
			if _, exists := s.records[key]; exists {
				return fmt.Errorf("duplicate sync state record %q", key)
			}
			s.records[key] = record
		}
	}
	s.initialized = true
	return nil
}

func (s *syncStateStore) writeRecordLocked(ctx context.Context, record serviceSyncState) error {
	return retrySyncStateWrite(func() error {
		configMap, err := s.getShardForWriteLocked(ctx, record.key())
		if err != nil {
			return err
		}
		records, err := decodeSyncStateRecords(configMap)
		if err != nil {
			return err
		}
		records[record.key()] = record
		if err = encodeSyncStateRecords(configMap, records); err != nil {
			return err
		}
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		return err
	})
}

func (s *syncStateStore) deleteRecordLocked(ctx context.Context, record serviceSyncState) error {
	return retrySyncStateWrite(func() error {
		configMap, err := s.getShardForWriteLocked(ctx, record.key())
		if err != nil {
			return err
		}
		records, err := decodeSyncStateRecords(configMap)
		if err != nil {
			return err
		}
		delete(records, record.key())
		if err = encodeSyncStateRecords(configMap, records); err != nil {
			return err
		}
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		return err
	})
}

func (s *syncStateStore) getShardForWriteLocked(
	ctx context.Context, recordKey string,
) (*v1.ConfigMap, error) {
	shard := syncStateShard(recordKey)
	name := syncStateConfigMapName(shard)
	configMap, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		return configMap, err
	}

	configMap = &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: s.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": polarisControllerName,
				syncStateLabelKey:              syncStateLabelValue,
			},
			Annotations: map[string]string{
				"polarismesh.cn/schema-version": "1",
			},
		},
		Data: map[string]string{syncStateDataKey: "{}"},
	}
	configMap, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
	}
	return configMap, err
}

func retrySyncStateWrite(fn func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = fn(); err == nil || !apierrors.IsConflict(err) {
			return err
		}
	}
	return err
}

func decodeSyncStateRecords(configMap *v1.ConfigMap) (map[string]serviceSyncState, error) {
	records := make(map[string]serviceSyncState)
	raw := configMap.Data[syncStateDataKey]
	if raw == "" {
		return records, nil
	}
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, err
	}
	if records == nil {
		records = make(map[string]serviceSyncState)
	}
	return records, nil
}

func encodeSyncStateRecords(configMap *v1.ConfigMap, records map[string]serviceSyncState) error {
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if len(raw) > syncStateMaxSize {
		return fmt.Errorf("sync state shard %s exceeds %d bytes", configMap.Name, syncStateMaxSize)
	}
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	configMap.Data[syncStateDataKey] = string(raw)
	return nil
}

func syncStateShard(key string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return int(hash.Sum32() % syncStateShardCount)
}

func syncStateConfigMapName(shard int) string {
	return fmt.Sprintf("%s%02x", syncStateNamePrefix, shard)
}

func (p *PolarisController) recoverSyncStates(ctx context.Context) error {
	if p.syncStateStore == nil {
		return nil
	}
	if err := p.syncStateStore.Refresh(ctx); err != nil {
		return err
	}
	records, err := p.syncStateStore.List(ctx)
	if err != nil {
		return err
	}
	services, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		return err
	}
	current := make(map[string]*v1.Service, len(services))
	for _, service := range services {
		current[service.GetNamespace()+"/"+service.GetName()] = service
	}

	for _, record := range records {
		if record.ClusterName != p.config.PolarisController.ClusterName {
			continue
		}
		service, exists := current[record.SourceNamespace+"/"+record.SourceName]
		active := exists &&
			service.GetDeletionTimestamp() == nil &&
			string(service.GetUID()) == record.SourceUID &&
			p.IsPolarisService(service)

		if active {
			if record.Phase != syncStateActive {
				log.SyncNamingScope().Infof("reactivate persisted sync state %s", record.key())
				if err = p.ensureActiveSyncState(service); err != nil {
					return err
				}
			}
			continue
		}

		if record.Phase != syncStatePendingDelete {
			log.SyncNamingScope().Warnf("mark persisted sync state %s pending deletion", record.key())
			record.Phase = syncStatePendingDelete
			record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			if err = p.syncStateStore.Upsert(ctx, record); err != nil {
				return err
			}
		}
		log.SyncNamingScope().Infof("enqueue persisted pending deletion %s", record.key())
		p.insertTask(record.sourceTask())
	}
	return nil
}
