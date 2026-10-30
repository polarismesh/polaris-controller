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
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
	"github.com/polarismesh/polaris-controller/pkg/util/address"
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
	syncStateTimeout    = 10 * time.Second
)

type serviceSyncState struct {
	ClusterName      string `json:"clusterName"`
	SourceNamespace  string `json:"sourceNamespace"`
	SourceName       string `json:"sourceName"`
	SourceUID        string `json:"sourceUID"`
	PolarisNamespace string `json:"polarisNamespace"`
	PolarisService   string `json:"polarisService"`
	Phase            string `json:"phase"`
	Revision         uint64 `json:"revision,omitempty"`
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
	initMu      sync.Mutex
	refreshMu   sync.RWMutex
	mu          sync.Mutex
	recordLocks sync.Map
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
	_, err := p.syncStateStore.Upsert(context.Background(), record)
	return err
}

func (p *PolarisController) markPendingSyncState(service *v1.Service) error {
	if p.syncStateStore == nil {
		return nil
	}
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStatePendingDelete)
	_, err := p.syncStateStore.Upsert(context.Background(), record)
	return err
}

func (p *PolarisController) deleteServiceWithState(service *v1.Service) error {
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStatePendingDelete)
	if p.syncStateStore != nil {
		var err error
		record, err = p.syncStateStore.Upsert(context.Background(), record)
		if err != nil {
			return fmt.Errorf("persist pending deletion for %s/%s: %w",
				service.GetNamespace(), service.GetName(), err)
		}
	}
	return p.processPendingSyncStateRecord(record)
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

	unlock := p.lockService(record.SourceNamespace, record.SourceName)
	defer unlock()

	record, ok, err = p.syncStateStore.Get(context.Background(), task.Name)
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
	return p.processPendingSyncStateRecord(record)
}

func (p *PolarisController) processPendingSyncStateRecord(record serviceSyncState) error {
	currentService, sourceActive, sameGeneration, err := p.currentServiceForSyncState(record)
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	if sourceActive && sameGeneration {
		log.SyncNamingScope().Infof(
			"Skip pending sync state %s deletion: current Service is active with the same UID",
			record.key())
		return p.ensureActiveSyncState(currentService)
	}

	service := record.service()
	instances, err := polarisapi.GetInstances(context.Background(), service)
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	candidates, protected, reactivated, err := p.pendingDeletionCandidates(record, instances)
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	if reactivated {
		return nil
	}

	if len(candidates) > 0 {
		if err = polarisapi.DeleteInstances(candidates, p.config.PolarisController.Size,
			fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())); err != nil {
			p.recordSyncStateFailure(record, err)
			return err
		}
	}
	if protected > 0 {
		log.SyncNamingScope().Infof("Keep pending sync state %s until %d protected instance(s) are taken over or deleted",
			record.key(), protected)
		return nil
	}

	// A successful DELETE (or an empty candidate set) is not a completion proof.
	instances, err = polarisapi.GetInstances(context.Background(), service)
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	candidates, protected, reactivated, err = p.pendingDeletionCandidates(record, instances)
	if err == nil && !reactivated && protected == 0 && len(candidates) > 0 {
		err = fmt.Errorf("pending deletion %s still has %d controller instance(s); keep state for retry",
			record.key(), len(candidates))
	}
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	if reactivated || protected > 0 {
		return nil
	}
	if p.syncStateStore == nil {
		return nil
	}
	removed, err := p.syncStateStore.RemoveIfMatch(context.Background(), record)
	if err != nil {
		return fmt.Errorf("remove sync state for %s: %w", record.key(), err)
	}
	if !removed {
		log.SyncNamingScope().Infof(
			"Keep sync state %s: revision changed while pending deletion was running", record.key())
	}
	return nil
}

func (p *PolarisController) pendingDeletionCandidates(
	record serviceSyncState, instances []polarisapi.Instance,
) ([]polarisapi.Instance, int, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		service, active, sameGeneration, err := p.currentServiceForSyncState(record)
		if err != nil {
			return nil, 0, false, err
		}
		if active && sameGeneration {
			log.SyncNamingScope().Infof("Skip pending sync state %s deletion: Service UID=%s became active",
				record.key(), service.GetUID())
			return nil, 0, true, p.ensureActiveSyncState(service)
		}

		var desired address.InstanceSet
		var endpoints *v1.Endpoints
		if active {
			desired, endpoints, err = p.desiredAddressesForService(service)
			if err != nil {
				return nil, 0, false, fmt.Errorf("verify current desired endpoints for %s: %w", record.key(), err)
			}
		}
		candidates := make([]polarisapi.Instance, 0, len(instances))
		protected := 0
		for _, instance := range instances {
			uid := instance.Metadata[util.K8sServiceUID]
			if !p.isControllerMetadata(instance.Metadata) || (uid != "" && uid != record.SourceUID) {
				continue
			}
			key := fmt.Sprintf("%s-%d", instance.Host, *instance.Port)
			if desired[key] != nil {
				protected++
				log.SyncNamingScope().Infof(
					"Skip deleting pending instance %s/%s host=%s port=%d oldUID=%s currentUID=%s: endpoint is still desired",
					record.SourceNamespace, record.SourceName, instance.Host, *instance.Port, record.SourceUID, service.GetUID())
				continue
			}
			candidates = append(candidates, polarisapi.Instance{
				ID: instance.ID, VpcID: instance.VpcID, Namespace: record.PolarisNamespace,
				Service: record.PolarisService, ServiceToken: globalToken, Host: instance.Host, Port: instance.Port,
			})
		}
		if protected > 0 {
			if err = p.ensureActiveSyncState(service); err != nil {
				return nil, 0, false, err
			}
			p.insertTask(&Task{Namespace: service.GetNamespace(), Name: service.GetName(), ObjectType: KubernetesService})
		}
		// Informer updates can happen during Pod reads and state persistence too.
		latest, latestActive, _, err := p.currentServiceForSyncState(record)
		if err != nil {
			return nil, 0, false, err
		}
		if latest != service || latestActive != active {
			continue
		}
		if active {
			latestEndpoints, getErr := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
			if getErr != nil {
				return nil, 0, false, getErr
			}
			if latestEndpoints != endpoints {
				continue
			}
		}
		return candidates, protected, false, nil
	}
	return nil, 0, false, fmt.Errorf("Service changed repeatedly while verifying pending deletion %s", record.key())
}

func (p *PolarisController) currentServiceForSyncState(
	record serviceSyncState,
) (*v1.Service, bool, bool, error) {
	service, err := p.serviceLister.Services(record.SourceNamespace).Get(record.SourceName)
	if apierrors.IsNotFound(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	sourceActive := false
	if service.GetDeletionTimestamp() == nil && !util.IgnoreService(service) {
		syncEnabled, explicit := service.GetAnnotations()[util.PolarisSync]
		switch {
		case explicit:
			sourceActive = syncEnabled == util.IsEnableSync
		case service.GetAnnotations()[util.PolarisEnableRegister] == util.IsEnableSync:
			sourceActive = true
		default:
			namespace, namespaceErr := p.namespaceLister.Get(service.GetNamespace())
			if namespaceErr != nil {
				return nil, false, false, fmt.Errorf("verify Namespace for pending deletion %s: %w", record.key(), namespaceErr)
			}
			sourceActive = p.IsNamespaceSyncEnable(namespace)
		}
	}
	return service, sourceActive, string(service.GetUID()) == record.SourceUID, nil
}

func (p *PolarisController) desiredAddressesForService(
	service *v1.Service,
) (address.InstanceSet, *v1.Endpoints, error) {
	endpoints, err := p.endpointsLister.Endpoints(service.GetNamespace()).Get(service.GetName())
	if err != nil {
		return nil, nil, fmt.Errorf("get current Endpoints %s/%s: %w", service.GetNamespace(), service.GetName(), err)
	}
	serviceMsg := fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())
	desired, err := address.GetAddressMapFromEndpoints(
		service, endpoints, p.podLister, getCustomWeight(service, serviceMsg))
	return desired, endpoints, err
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
	_, changed, err := p.syncStateStore.UpdateIfMatch(context.Background(), record,
		func(current *serviceSyncState) {
			current.Attempts++
			current.LastError = failure.Error()
			current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		})
	if err != nil {
		log.SyncNamingScope().Errorf("failed to persist sync state error for %s: %v", record.key(), err)
	}
	if err == nil && !changed {
		log.SyncNamingScope().Infof(
			"Skip recording failure for stale sync state %s revision=%d", record.key(), record.Revision)
	}
}

func (s *syncStateStore) Upsert(
	ctx context.Context, record serviceSyncState,
) (serviceSyncState, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return serviceSyncState{}, err
	}
	s.refreshMu.RLock()
	defer s.refreshMu.RUnlock()

	unlock := s.lockRecord(record.key())
	defer unlock()

	existing, found := s.getLocal(record.key())
	if found && sameSyncStateContent(existing, record) {
		return existing, nil
	}
	if found {
		record.Revision = existing.Revision + 1
	} else {
		record.Revision = 1
	}
	if record.UpdatedAt == "" {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if _, err := s.writeRecordIfMatchLocked(ctx, record, nil); err != nil {
		return serviceSyncState{}, err
	}
	s.setLocal(record)
	return record, nil
}

func (s *syncStateStore) UpdateIfMatch(
	ctx context.Context, expected serviceSyncState, update func(*serviceSyncState),
) (serviceSyncState, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return serviceSyncState{}, false, err
	}
	s.refreshMu.RLock()
	defer s.refreshMu.RUnlock()

	unlock := s.lockRecord(expected.key())
	defer unlock()

	current, found := s.getLocal(expected.key())
	if !found || !sameSyncStateVersion(current, expected) {
		return current, false, nil
	}
	updated := current
	update(&updated)
	if sameSyncStateContent(current, updated) {
		return current, false, nil
	}
	updated.Revision = current.Revision + 1
	if updated.UpdatedAt == "" {
		updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	changed, err := s.writeRecordIfMatchLocked(ctx, updated, &current)
	if err != nil {
		return serviceSyncState{}, false, err
	}
	if !changed {
		return current, false, nil
	}
	s.setLocal(updated)
	return updated, true, nil
}

func (s *syncStateStore) Remove(ctx context.Context, record serviceSyncState) error {
	_, err := s.RemoveIfMatch(ctx, record)
	return err
}

func (s *syncStateStore) RemoveIfMatch(
	ctx context.Context, expected serviceSyncState,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return false, err
	}
	s.refreshMu.RLock()
	defer s.refreshMu.RUnlock()

	unlock := s.lockRecord(expected.key())
	defer unlock()

	current, found := s.getLocal(expected.key())
	if !found {
		return false, nil
	}
	if !sameSyncStateVersion(current, expected) {
		return false, nil
	}
	removed, err := s.deleteRecordIfMatchLocked(ctx, current)
	if err != nil {
		return false, err
	}
	if removed {
		s.deleteLocal(current.key())
	}
	return removed, nil
}

func (s *syncStateStore) Get(ctx context.Context, key string) (serviceSyncState, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return serviceSyncState{}, false, err
	}
	record, ok := s.getLocal(key)
	return record, ok, nil
}

func (s *syncStateStore) List(ctx context.Context) ([]serviceSyncState, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
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
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.initMu.Lock()
	defer s.initMu.Unlock()
	records, err := s.loadRecords(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.records = records
	s.initialized = true
	s.mu.Unlock()
	return nil
}

func (s *syncStateStore) MarkPendingForSource(
	ctx context.Context, clusterName, namespace, name string,
) ([]serviceSyncState, error) {
	ctx, cancel := context.WithTimeout(ctx, syncStateTimeout)
	defer cancel()
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	snapshot := make([]serviceSyncState, 0)
	for _, record := range s.records {
		if record.ClusterName == clusterName &&
			record.SourceNamespace == namespace && record.SourceName == name {
			snapshot = append(snapshot, record)
		}
	}
	s.mu.Unlock()

	pending := make([]serviceSyncState, 0, len(snapshot))
	for _, record := range snapshot {
		if record.Phase == syncStatePendingDelete {
			pending = append(pending, record)
			continue
		}
		updated, changed, err := s.UpdateIfMatch(ctx, record, func(current *serviceSyncState) {
			current.Phase = syncStatePendingDelete
			current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		})
		if err != nil {
			return nil, err
		}
		if changed {
			record = updated
		} else {
			current, found, getErr := s.Get(ctx, record.key())
			if getErr != nil {
				return nil, getErr
			}
			if !found || current.Phase != syncStatePendingDelete {
				continue
			}
			record = current
		}
		pending = append(pending, record)
	}
	return pending, nil
}

func (s *syncStateStore) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	initialized := s.initialized
	s.mu.Unlock()
	if initialized {
		return nil
	}

	s.initMu.Lock()
	defer s.initMu.Unlock()

	s.mu.Lock()
	initialized = s.initialized
	s.mu.Unlock()
	if initialized {
		return nil
	}

	records, err := s.loadRecords(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.records = records
	s.initialized = true
	s.mu.Unlock()
	return nil
}

func (s *syncStateStore) loadRecords(ctx context.Context) (map[string]serviceSyncState, error) {
	if s.client == nil {
		return nil, fmt.Errorf("kubernetes client is not configured")
	}
	records := make(map[string]serviceSyncState)

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
					log.SyncNamingScope().Errorf("Failed to create sync state ConfigMap %s/%s: %v",
						s.namespace, name, err)
					return nil, err
				}
				configMap, err = s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					log.SyncNamingScope().Errorf("Failed to read existing sync state ConfigMap %s/%s: %v",
						s.namespace, name, err)
					return nil, err
				}
			} else {
				log.SyncNamingScope().Infof("Created sync state ConfigMap %s/%s", s.namespace, name)
			}
		} else if err != nil {
			log.SyncNamingScope().Errorf("Failed to get sync state ConfigMap %s/%s: %v",
				s.namespace, name, err)
			return nil, err
		}

		shardRecords, err := decodeSyncStateRecords(configMap)
		if err != nil {
			log.SyncNamingScope().Errorf("Failed to decode sync state ConfigMap %s/%s: %v",
				s.namespace, name, err)
			return nil, fmt.Errorf("decode sync state ConfigMap %s/%s: %w", s.namespace, name, err)
		}
		for key, record := range shardRecords {
			if _, exists := records[key]; exists {
				log.SyncNamingScope().Errorf("Duplicate sync state record %q in ConfigMap %s/%s",
					key, s.namespace, name)
				return nil, fmt.Errorf("duplicate sync state record %q", key)
			}
			records[key] = record
		}
	}
	return records, nil
}

func (s *syncStateStore) writeRecordIfMatchLocked(
	ctx context.Context, record serviceSyncState, expected *serviceSyncState,
) (bool, error) {
	written := false
	err := retrySyncStateWrite(func() error {
		written = false
		configMap, err := s.getShardForWriteLocked(ctx, record.key())
		if err != nil {
			return err
		}
		records, err := decodeSyncStateRecords(configMap)
		if err != nil {
			return err
		}
		if expected != nil {
			current, exists := records[record.key()]
			if !exists || !sameSyncStateVersion(current, *expected) {
				return nil
			}
		}
		records[record.key()] = record
		if err = encodeSyncStateRecords(configMap, records); err != nil {
			return err
		}
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		written = err == nil
		return err
	})
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to persist sync state %s phase=%s: %v",
			record.key(), record.Phase, err)
		return false, err
	}
	if !written {
		return false, nil
	}
	log.SyncNamingScope().Infof("Persisted sync state %s phase=%s revision=%d attempts=%d",
		record.key(), record.Phase, record.Revision, record.Attempts)
	return true, nil
}

func (s *syncStateStore) deleteRecordIfMatchLocked(
	ctx context.Context, record serviceSyncState,
) (bool, error) {
	removed := false
	err := retrySyncStateWrite(func() error {
		removed = false
		configMap, err := s.getShardForWriteLocked(ctx, record.key())
		if err != nil {
			return err
		}
		records, err := decodeSyncStateRecords(configMap)
		if err != nil {
			return err
		}
		current, exists := records[record.key()]
		if !exists {
			removed = true
			return nil
		}
		if !sameSyncStateVersion(current, record) {
			return nil
		}
		delete(records, record.key())
		if err = encodeSyncStateRecords(configMap, records); err != nil {
			return err
		}
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		removed = err == nil
		return err
	})
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to remove sync state %s: %v", record.key(), err)
		return false, err
	}
	if !removed {
		return false, nil
	}
	log.SyncNamingScope().Infof("Removed sync state %s revision=%d", record.key(), record.Revision)
	return true, nil
}

func (s *syncStateStore) getLocal(key string) (serviceSyncState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[key]
	return record, ok
}

func (s *syncStateStore) setLocal(record serviceSyncState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.key()] = record
}

func (s *syncStateStore) deleteLocal(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, key)
}

func (s *syncStateStore) lockRecord(key string) func() {
	value, _ := s.recordLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func sameSyncStateVersion(current, expected serviceSyncState) bool {
	return current.Revision == expected.Revision && sameSyncStateContent(current, expected)
}

func sameSyncStateContent(current, expected serviceSyncState) bool {
	return current.ClusterName == expected.ClusterName &&
		current.SourceNamespace == expected.SourceNamespace &&
		current.SourceName == expected.SourceName &&
		current.SourceUID == expected.SourceUID &&
		current.PolarisNamespace == expected.PolarisNamespace &&
		current.PolarisService == expected.PolarisService &&
		current.Phase == expected.Phase &&
		current.Attempts == expected.Attempts &&
		current.LastError == expected.LastError
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
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to create sync state shard ConfigMap %s/%s: %v",
			s.namespace, name, err)
		return nil, err
	}
	log.SyncNamingScope().Infof("Created sync state shard ConfigMap %s/%s", s.namespace, name)
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
			updated, changed, updateErr := p.syncStateStore.UpdateIfMatch(ctx, record,
				func(current *serviceSyncState) {
					current.Phase = syncStatePendingDelete
					current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				})
			if updateErr != nil {
				return updateErr
			}
			if changed {
				record = updated
			}
			if !changed {
				log.SyncNamingScope().Infof(
					"skip enqueue for sync state %s whose revision changed during recovery", record.key())
				continue
			}
		}
		log.SyncNamingScope().Infof("enqueue persisted pending deletion %s", record.key())
		p.insertTask(record.sourceTask())
	}
	return nil
}
