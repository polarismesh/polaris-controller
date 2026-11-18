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
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
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
	syncStateErrorLimit = 4096
)

type serviceSyncState struct {
	ClusterName      string            `json:"clusterName"`
	SourceNamespace  string            `json:"sourceNamespace"`
	SourceName       string            `json:"sourceName"`
	SourceUID        string            `json:"sourceUID"`
	PolarisNamespace string            `json:"polarisNamespace"`
	PolarisService   string            `json:"polarisService"`
	Phase            string            `json:"phase"`
	AppliedLabels    map[string]string `json:"appliedLabels,omitempty"`
	Revision         uint64            `json:"revision,omitempty"`
	Attempts         int               `json:"attempts,omitempty"`
	LastError        string            `json:"lastError,omitempty"`
	UpdatedAt        string            `json:"updatedAt"`
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
	_, err := p.transitionSyncState(service, syncStateActive)
	return err
}

// Change only the phase: a retry or reactivation must retain the independently
// applied Labels and the deletion history for this Service UID.
func (p *PolarisController) transitionSyncState(service *v1.Service, phase string) (serviceSyncState, error) {
	record := newServiceSyncState(service, p.config.PolarisController.ClusterName, phase)
	if p.syncStateStore == nil {
		return record, nil
	}
	existing, found, err := p.syncStateStore.Get(p.requestContext(), record.key())
	if err != nil {
		return serviceSyncState{}, err
	}
	if !found {
		return p.syncStateStore.Upsert(p.requestContext(), record)
	}
	updated, _, err := p.syncStateStore.UpdateIfMatch(p.requestContext(), existing,
		func(current *serviceSyncState) {
			current.Phase = phase
			current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		})
	if err == nil && updated.Phase != phase {
		err = fmt.Errorf("sync state %s changed while transitioning to %s", record.key(), phase)
	}
	return updated, err
}

func (p *PolarisController) appliedServiceFromSyncState(service *v1.Service) (*v1.Service, error) {
	baseline := service.DeepCopy()
	baseline.Labels = nil
	if p.syncStateStore == nil {
		return baseline, nil
	}
	key := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStateActive).key()
	record, ok, err := p.syncStateStore.Get(p.requestContext(), key)
	if err != nil {
		return nil, err
	}
	if ok {
		baseline.Labels = maps.Clone(record.AppliedLabels)
	}
	return baseline, nil
}

func (p *PolarisController) persistAppliedServiceLabels(service *v1.Service, appliedLabels map[string]string) error {
	if p.syncStateStore == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(p.requestContext(), syncStateTimeout)
	defer cancel()
	key := newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStateActive).key()

	for attempt := 0; attempt < 3; attempt++ {
		record, ok, err := p.syncStateStore.Get(ctx, key)
		if err != nil {
			return err
		}
		if !ok {
			record = newServiceSyncState(service, p.config.PolarisController.ClusterName, syncStateActive)
			if appliedLabels == nil {
				record.AppliedLabels = make(map[string]string)
			} else {
				record.AppliedLabels = maps.Clone(appliedLabels)
			}
			_, err = p.syncStateStore.Upsert(ctx, record)
			return err
		}
		if record.Phase != syncStateActive {
			return fmt.Errorf(
				"cannot persist applied labels for sync state %s in phase %s",
				record.key(), record.Phase)
		}

		updated, _, err := p.syncStateStore.UpdateIfMatch(ctx, record,
			func(current *serviceSyncState) {
				current.AppliedLabels = maps.Clone(appliedLabels)
				current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			})
		if err != nil {
			return err
		}
		if maps.Equal(updated.AppliedLabels, appliedLabels) {
			return nil
		}
	}

	return fmt.Errorf("sync state %s changed repeatedly while persisting applied labels", key)
}

func (p *PolarisController) markPendingSyncState(service *v1.Service) error {
	_, err := p.transitionSyncState(service, syncStatePendingDelete)
	return err
}

func (p *PolarisController) deleteServiceWithState(service *v1.Service) error {
	record, err := p.transitionSyncState(service, syncStatePendingDelete)
	if err != nil {
		return fmt.Errorf("persist pending deletion for %s/%s: %w",
			service.GetNamespace(), service.GetName(), err)
	}
	return p.processPendingSyncStateRecord(record)
}

func (p *PolarisController) processPendingSyncState(task *Task) error {
	if p.syncStateStore == nil {
		return nil
	}
	record, ok, err := p.syncStateStore.Get(p.requestContext(), task.Name)
	if err != nil {
		return err
	}
	if !ok || record.Phase != syncStatePendingDelete {
		return nil
	}

	unlock := p.lockService(record.SourceNamespace, record.SourceName)
	defer unlock()

	record, ok, err = p.syncStateStore.Get(p.requestContext(), task.Name)
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
	instances, err := polarisapi.GetInstances(p.requestContext(), service)
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

	var deletionConflict error
	if len(candidates) > 0 {
		if err = polarisapi.DeleteInstancesWithContext(p.requestContext(), candidates, p.config.PolarisController.Size,
			fmt.Sprintf("[%s/%s]", service.GetNamespace(), service.GetName())); err != nil {
			if !errors.Is(err, polarisapi.ErrDeleteConflict) {
				p.recordSyncStateFailure(record, err)
				return err
			}
			deletionConflict = err
		}
	}
	if protected > 0 {
		log.SyncNamingScope().Infof("Keep pending sync state %s until %d protected instance(s) are taken over or deleted",
			record.key(), protected)
		return nil
	}

	// A successful DELETE (or an empty candidate set) is not a completion proof.
	instances, err = polarisapi.GetInstances(p.requestContext(), service)
	if err != nil {
		p.recordSyncStateFailure(record, err)
		return err
	}
	candidates, protected, reactivated, err = p.pendingDeletionCandidates(record, instances)
	if err == nil && !reactivated && protected == 0 && len(candidates) > 0 {
		if deletionConflict != nil {
			err = deletionConflict
		} else {
			err = fmt.Errorf("pending deletion %s still has %d controller instance(s); keep state for retry",
				record.key(), len(candidates))
		}
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
	removed, err := p.syncStateStore.RemoveIfMatch(p.requestContext(), record)
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
			key := address.InstanceKey(instance.VpcID, instance.Host, *instance.Port)
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
	records, err := p.syncStateStore.MarkPendingForSource(p.requestContext(),
		p.config.PolarisController.ClusterName, namespace, name)
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		// A state write may have committed even when its response was lost.
		// Confirm the durable ledger before treating a local cache miss as absent.
		if err = p.syncStateStore.Refresh(p.requestContext()); err != nil {
			return 0, err
		}
		records, err = p.syncStateStore.MarkPendingForSource(p.requestContext(),
			p.config.PolarisController.ClusterName, namespace, name)
		if err != nil {
			return 0, err
		}
	}
	for _, record := range records {
		p.insertTask(record.sourceTask())
	}
	return len(records), nil
}

func (p *PolarisController) recordSyncStateFailure(record serviceSyncState, failure error) {
	if p.syncStateStore == nil || p.requestContext().Err() != nil {
		return
	}
	_, changed, err := p.syncStateStore.UpdateIfMatch(p.requestContext(), record,
		func(current *serviceSyncState) {
			current.Attempts++
			current.LastError = syncStateErrorSummary(failure.Error())
			current.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		})
	if err != nil {
		log.SyncNamingScope().ReportFailure(
			"sync-state-error/"+record.key(), polarisapi.FailureSignature(err), err, false, false)
	} else {
		log.SyncNamingScope().ClearFailure("sync-state-error/" + record.key())
	}
	if err == nil && !changed {
		log.SyncNamingScope().Infof(
			"Skip recording failure for stale sync state %s revision=%d", record.key(), record.Revision)
	}
}

func syncStateErrorSummary(message string) string {
	message = strings.ToValidUTF8(message, "\uFFFD")
	if len(message) <= syncStateErrorLimit {
		return message
	}
	const suffix = "... [truncated]"
	end := syncStateErrorLimit - len(suffix)
	for !utf8.RuneStart(message[end]) {
		end--
	}
	return message[:end] + suffix
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

	// A local cache miss can be caused by a shard that failed to load. Read
	// the durable record first so a transient read failure cannot replace its
	// revision and deletion history with a new revision-1 record.
	configMap, err := s.getShardForWriteLocked(ctx, record.key())
	if err != nil {
		return serviceSyncState{}, err
	}
	shardRecords, err := decodeSyncStateRecords(configMap)
	if err != nil {
		return serviceSyncState{}, err
	}
	durable, exists := shardRecords[record.key()]
	if exists {
		merged := mergeUpsertRecord(durable, record)
		if sameSyncStateContent(durable, merged) {
			s.setLocal(durable)
			return durable, nil
		}
		merged.Revision = durable.Revision + 1
		if merged.UpdatedAt == "" {
			merged.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		changed, writeErr := s.writeRecordIfMatchLocked(ctx, merged, &durable)
		if writeErr != nil {
			return serviceSyncState{}, writeErr
		}
		if !changed {
			return serviceSyncState{}, fmt.Errorf("sync state %s changed while upserting", record.key())
		}
		s.setLocal(merged)
		return merged, nil
	}

	record.Revision = 1
	if record.UpdatedAt == "" {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	changed, writeErr := s.writeRecordIfMatchLocked(ctx, record, nil)
	if writeErr != nil {
		return serviceSyncState{}, writeErr
	}
	if !changed {
		return serviceSyncState{}, fmt.Errorf("sync state %s appeared while upserting", record.key())
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
		durable, durableFound, durableErr := s.loadDurableRecordLocked(ctx, expected.key())
		if durableErr != nil {
			return serviceSyncState{}, false, durableErr
		}
		if durableFound {
			s.setLocal(durable)
			current = durable
			found = true
		}
		if !found || !sameSyncStateVersion(current, expected) {
			return current, false, nil
		}
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
		durable, durableFound, durableErr := s.loadDurableRecordLocked(ctx, expected.key())
		if durableErr != nil {
			return serviceSyncState{}, false, durableErr
		}
		if durableFound {
			s.setLocal(durable)
			return durable, false, nil
		}
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
	_, err := s.refreshShards(ctx)
	if err != nil {
		log.SyncNamingScope().ReportFailure(
			"sync-state-load/"+s.namespace, polarisapi.FailureSignature(err), err, false, false)
		s.mu.Lock()
		empty := len(s.records) == 0
		s.mu.Unlock()
		if empty {
			return err
		}
	} else {
		log.SyncNamingScope().ClearFailure("sync-state-load/" + s.namespace)
	}
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

	loadedAny, err := s.refreshShards(ctx)
	s.mu.Lock()
	initialized = loadedAny
	s.initialized = initialized
	s.mu.Unlock()
	if err != nil {
		log.SyncNamingScope().ReportFailure(
			"sync-state-load/"+s.namespace, polarisapi.FailureSignature(err), err, false, false)
		if !initialized {
			return err
		}
		return nil
	}
	log.SyncNamingScope().ClearFailure("sync-state-load/" + s.namespace)
	return nil
}

func (s *syncStateStore) refreshShards(ctx context.Context) (bool, error) {
	if s.client == nil {
		return false, fmt.Errorf("kubernetes client is not configured")
	}
	var shardErrs []error
	loadedAny := false
	for shard := 0; shard < syncStateShardCount; shard++ {
		records, err := s.loadShard(ctx, shard)
		if err != nil {
			shardErrs = append(shardErrs, err)
			continue
		}
		loadedAny = true

		s.mu.Lock()
		s.replaceShardRecordsLocked(shard, records)
		s.mu.Unlock()
	}
	return loadedAny, errors.Join(shardErrs...)
}

func (s *syncStateStore) loadShard(ctx context.Context, shard int) (map[string]serviceSyncState, error) {
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
				log.SyncNamingScope().Debugf("Failed to create sync state ConfigMap %s/%s: %v",
					s.namespace, name, err)
				return nil, fmt.Errorf("create sync state ConfigMap %s/%s: %w", s.namespace, name, err)
			}
			configMap, err = s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				log.SyncNamingScope().Debugf("Failed to read existing sync state ConfigMap %s/%s: %v",
					s.namespace, name, err)
				return nil, fmt.Errorf("read sync state ConfigMap %s/%s: %w", s.namespace, name, err)
			}
		} else {
			log.SyncNamingScope().Infof("Created sync state ConfigMap %s/%s", s.namespace, name)
		}
	} else if err != nil {
		log.SyncNamingScope().Debugf("Failed to get sync state ConfigMap %s/%s: %v",
			s.namespace, name, err)
		return nil, fmt.Errorf("get sync state ConfigMap %s/%s: %w", s.namespace, name, err)
	}

	records, err := decodeSyncStateRecords(configMap)
	if err != nil {
		log.SyncNamingScope().Debugf("Failed to decode sync state ConfigMap %s/%s: %v",
			s.namespace, name, err)
		return nil, fmt.Errorf("decode sync state ConfigMap %s/%s: %w", s.namespace, name, err)
	}
	for key := range records {
		if syncStateShard(key) != shard {
			return nil, fmt.Errorf("sync state record %q is stored in wrong shard %d", key, shard)
		}
	}
	return records, nil
}

func (s *syncStateStore) replaceShardRecordsLocked(shard int, records map[string]serviceSyncState) {
	for key := range s.records {
		if syncStateShard(key) == shard {
			delete(s.records, key)
		}
	}
	for key, record := range records {
		s.records[key] = record
	}
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
		} else if _, exists := records[record.key()]; exists {
			return nil
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
		// Kubernetes updates can commit and still lose the response. Read the
		// durable shard before returning an unknown outcome to the caller.
		durable, found, durableErr := s.loadDurableRecordLocked(ctx, record.key())
		if durableErr != nil {
			return false, errors.Join(err, durableErr)
		}
		if found && sameSyncStateVersion(durable, record) {
			s.setLocal(durable)
			return true, nil
		}
		log.SyncNamingScope().Debugf("Failed to persist sync state %s phase=%s: %v",
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

func (s *syncStateStore) loadDurableRecordLocked(
	ctx context.Context, recordKey string,
) (serviceSyncState, bool, error) {
	configMap, err := s.getShardForWriteLocked(ctx, recordKey)
	if err != nil {
		return serviceSyncState{}, false, err
	}
	records, err := decodeSyncStateRecords(configMap)
	if err != nil {
		return serviceSyncState{}, false, err
	}
	record, found := records[recordKey]
	return record, found, nil
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
		log.SyncNamingScope().Debugf("Failed to remove sync state %s: %v", record.key(), err)
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
		maps.Equal(current.AppliedLabels, expected.AppliedLabels) &&
		current.Attempts == expected.Attempts &&
		current.LastError == expected.LastError
}

func mergeUpsertRecord(current, desired serviceSyncState) serviceSyncState {
	merged := desired
	if desired.AppliedLabels == nil {
		merged.AppliedLabels = maps.Clone(current.AppliedLabels)
	}
	merged.Attempts = current.Attempts
	merged.LastError = current.LastError
	merged.Revision = current.Revision
	return merged
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
		log.SyncNamingScope().Debugf("Failed to create sync state shard ConfigMap %s/%s: %v",
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
	pendingCount := 0
	for _, record := range records {
		if record.ClusterName == p.config.PolarisController.ClusterName && record.Phase == syncStatePendingDelete {
			pendingCount++
		}
	}
	metrics.PendingDeletions.WithLabelValues(p.config.PolarisController.ClusterName).Set(float64(pendingCount))
	services, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		return err
	}
	current := make(map[string]*v1.Service, len(services))
	for _, service := range services {
		current[service.GetNamespace()+"/"+service.GetName()] = service
	}

	var recoveryErrs []error
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
					recoveryErrs = append(recoveryErrs, fmt.Errorf("reactivate sync state %s: %w", record.key(), err))
					continue
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
				recoveryErrs = append(recoveryErrs, fmt.Errorf("mark sync state %s pending: %w", record.key(), updateErr))
				continue
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
	return errors.Join(recoveryErrs...)
}
