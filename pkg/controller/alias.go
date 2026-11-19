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
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/polarismesh/polaris-controller/common/log"
	"github.com/polarismesh/polaris-controller/pkg/metrics"
	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

type aliasKey struct {
	Namespace string
	Service   string
}

func (k aliasKey) String() string {
	return k.Namespace + "/" + k.Service
}

type aliasClaim struct {
	Namespace string
	Name      string
	UID       string
	CreatedAt int64
}

const aliasProvenancePrefix = "polaris-controller:v1:"

// Console authentication replaces Owners. Comment is stored unchanged, so bind
// this provenance to the cluster, source UID, alias name and actual target.
type aliasProvenance struct {
	Cluster        string `json:"cluster"`
	SourceUID      string `json:"uid"`
	Namespace      string `json:"namespace"`
	Service        string `json:"service"`
	AliasNamespace string `json:"aliasNamespace"`
	Alias          string `json:"alias"`
}

func serviceAliasKey(service *v1.Service) (aliasKey, bool) {
	namespace := service.GetAnnotations()[util.PolarisAliasNamespace]
	name := service.GetAnnotations()[util.PolarisAliasService]
	if namespace == "" || name == "" {
		return aliasKey{}, false
	}
	return aliasKey{Namespace: namespace, Service: name}, true
}

func (p *PolarisController) reconcileServiceAlias(service *v1.Service) error {
	key, ok := serviceAliasKey(service)
	if !ok || !p.IsPolarisService(service) {
		return nil
	}

	unlock := p.lockAlias(key)
	defer unlock()

	claims, err := p.aliasClaims(key, service)
	if err != nil {
		return err
	}
	if len(claims) == 0 {
		return fmt.Errorf("no active service claims alias %s", key.String())
	}

	winner := claims[0]
	if !isAliasClaim(service, winner) {
		p.reportAliasWaiting(service, key, winner, claims)
		return nil
	}

	actual, found, err := polarisapi.GetServiceAliasWithContext(p.requestContext(), key.Namespace, key.Service)
	if err != nil {
		return fmt.Errorf("get alias %s: %w", key.String(), err)
	}
	if !found {
		created, err := polarisapi.CreateServiceAliasWithContext(p.requestContext(), service,
			p.aliasOwner(service), p.aliasComment(service))
		if err != nil {
			return fmt.Errorf("create alias %s: %w", key.String(), err)
		}
		if created.Code == polarisapi.ExistedResource {
			// The alias namespace/name is occupied by an ordinary Service.
			// Instance registration must continue; alias creation is retried by resync.
			p.clearAliasClaimState(service, key)
			return nil
		}
		if err = p.verifyServiceAlias(service, key); err != nil {
			return err
		}
		p.clearAliasClaimState(service, key)
		return nil
	}

	if actual.Service == service.GetName() && actual.Namespace == service.GetNamespace() {
		p.clearAliasClaimState(service, key)
		return nil
	}
	if !p.canUpdateAlias(service, actual) {
		p.reportAliasOwnershipConflict(service, key, actual)
		return nil
	}

	// Preserve the server's authenticated owner; provenance is independent of it.
	actual.Comment = p.aliasComment(service)
	if _, err = polarisapi.UpdateServiceAliasWithContext(p.requestContext(), service, actual, actual.Owners); err != nil {
		return fmt.Errorf("update alias %s: %w", key.String(), err)
	}
	if err = p.verifyServiceAlias(service, key); err != nil {
		return err
	}
	p.clearAliasClaimState(service, key)
	return nil
}

// aliasOwnerSegmentMax matches owner_service_map.owner, which the Polaris server
// defines as varchar(32). The server splits Owners on , and ; before storing
// each segment, so every generated segment must stay within this limit.
const aliasOwnerSegmentMax = 32

func (p *PolarisController) aliasOwner(_ *v1.Service) string {
	// Keep the public owner cluster-scoped. The exact source Service remains in
	// the provenance Comment; Owners may be replaced by server authentication.
	return strings.Join([]string{
		polarisapi.Source,
		aliasOwnerSegment(p.config.PolarisController.ClusterName),
	}, ",")
}

// aliasOwnerSegment keeps one owner segment short and free of separators that
// the server could split into further segments.
func aliasOwnerSegment(value string) string {
	runes := make([]rune, 0, len(value))
	for _, r := range value {
		switch r {
		case ',', ';', '/':
			runes = append(runes, '-')
		default:
			runes = append(runes, r)
		}
	}
	if len(runes) > aliasOwnerSegmentMax {
		runes = runes[:aliasOwnerSegmentMax]
	}
	return string(runes)
}

func (p *PolarisController) canUpdateAlias(service *v1.Service, actual polarisapi.Alias) bool {
	if strings.HasPrefix(actual.Comment, aliasProvenancePrefix) {
		var provenance aliasProvenance
		if err := json.Unmarshal([]byte(strings.TrimPrefix(actual.Comment, aliasProvenancePrefix)), &provenance); err != nil {
			return false
		}
		key, valid := serviceAliasKey(service)
		return valid && provenance.Cluster == p.config.PolarisController.ClusterName &&
			provenance.SourceUID != "" && provenance.Namespace == actual.Namespace &&
			provenance.Service == actual.Service && provenance.AliasNamespace == key.Namespace &&
			provenance.Alias == key.Service && actual.AliasNamespace == key.Namespace && actual.Alias == key.Service
	}
	if actual.Owners == p.aliasOwner(service) {
		return true
	}
	return false
}

func (p *PolarisController) aliasComment(service *v1.Service) string {
	key, _ := serviceAliasKey(service)
	provenance := aliasProvenance{
		Cluster: p.config.PolarisController.ClusterName, SourceUID: string(service.GetUID()),
		Namespace: service.GetNamespace(), Service: service.GetName(),
		AliasNamespace: key.Namespace, Alias: key.Service,
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		// The provenance contains only strings. An encoding failure must never
		// produce a marker that could authorize an alias takeover.
		return ""
	}
	return aliasProvenancePrefix + string(encoded)
}

func (p *PolarisController) enqueueOtherAliasClaimants(service *v1.Service, eventType string) {
	key, ok := serviceAliasKey(service)
	if !ok {
		return
	}
	services, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		log.SyncNamingScope().Errorf("Failed to list alias claimants for %s: %v", key.String(), err)
		return
	}
	for _, claimant := range services {
		if claimant.GetNamespace() == service.GetNamespace() && claimant.GetName() == service.GetName() {
			continue
		}
		if claimant.GetDeletionTimestamp() != nil || !p.isActiveAliasClaim(claimant, key) {
			continue
		}
		task := &Task{
			Namespace:  claimant.GetNamespace(),
			Name:       claimant.GetName(),
			ObjectType: KubernetesService,
			Operation:  OperationUpdate,
		}
		p.enqueueService(task, claimant, eventType)
	}
}

func (p *PolarisController) verifyServiceAlias(service *v1.Service, key aliasKey) error {
	actual, found, err := polarisapi.GetServiceAliasWithContext(p.requestContext(), key.Namespace, key.Service)
	if err != nil {
		return fmt.Errorf("verify alias %s: %w", key.String(), err)
	}
	if !found {
		return fmt.Errorf("alias %s not found after write", key.String())
	}
	if actual.Service != service.GetName() || actual.Namespace != service.GetNamespace() {
		return fmt.Errorf("alias %s points to %s/%s, want %s/%s", key.String(),
			actual.Namespace, actual.Service, service.GetNamespace(), service.GetName())
	}
	return nil
}

func (p *PolarisController) aliasClaims(key aliasKey, current *v1.Service) ([]aliasClaim, error) {
	services, err := p.serviceLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	claims := make([]aliasClaim, 0, len(services))
	hasCurrent := false
	for _, service := range services {
		if service.GetDeletionTimestamp() != nil || !p.isActiveAliasClaim(service, key) {
			continue
		}
		if service.GetNamespace() == current.GetNamespace() && service.GetName() == current.GetName() {
			hasCurrent = true
		}
		claims = append(claims, aliasClaim{
			Namespace: service.GetNamespace(),
			Name:      service.GetName(),
			UID:       string(service.GetUID()),
			CreatedAt: service.GetCreationTimestamp().UnixNano(),
		})
	}
	if !hasCurrent && p.isActiveAliasClaim(current, key) {
		claims = append(claims, aliasClaim{
			Namespace: current.GetNamespace(),
			Name:      current.GetName(),
			UID:       string(current.GetUID()),
			CreatedAt: current.GetCreationTimestamp().UnixNano(),
		})
	}

	sort.Slice(claims, func(i, j int) bool {
		if claims[i].CreatedAt == claims[j].CreatedAt {
			if claims[i].UID != claims[j].UID {
				return claims[i].UID < claims[j].UID
			}
			if claims[i].Namespace != claims[j].Namespace {
				return claims[i].Namespace < claims[j].Namespace
			}
			return claims[i].Name < claims[j].Name
		}
		return claims[i].CreatedAt < claims[j].CreatedAt
	})
	return claims, nil
}

func (p *PolarisController) isActiveAliasClaim(service *v1.Service, key aliasKey) bool {
	candidateKey, ok := serviceAliasKey(service)
	if !ok || candidateKey != key || !p.IsPolarisService(service) {
		return false
	}
	return service.GetDeletionTimestamp() == nil
}

func isAliasClaim(service *v1.Service, claim aliasClaim) bool {
	return service.GetNamespace() == claim.Namespace && service.GetName() == claim.Name &&
		string(service.GetUID()) == claim.UID
}

func (p *PolarisController) lockAlias(key aliasKey) func() {
	value, _ := p.aliasLocks.LoadOrStore(key.String(), &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (p *PolarisController) reportAliasWaiting(
	service *v1.Service, key aliasKey, winner aliasClaim, claims []aliasClaim,
) {
	claimIDs := make([]string, 0, len(claims))
	for _, claim := range claims {
		claimIDs = append(claimIDs, claim.Namespace+"/"+claim.Name+"#"+claim.UID)
	}
	state := "waiting:" + strings.Join(claimIDs, ",")
	if !p.setAliasClaimState(service, key, state) {
		return
	}

	message := fmt.Sprintf("alias %s is waiting; %s/%s is the first claimant",
		key.String(), winner.Namespace, winner.Name)
	log.SyncNamingScope().Warnf("%s/%s: %s", service.GetNamespace(), service.GetName(), message)
	p.eventRecorder.Eventf(service, v1.EventTypeWarning, "AliasClaimWaiting", "%s", message)
	metrics.AliasBindingConflicts.WithLabelValues(
		key.Namespace, key.Service, service.GetNamespace(), service.GetName(), "waiting").Inc()
}

func (p *PolarisController) reportAliasOwnershipConflict(
	service *v1.Service, key aliasKey, actual polarisapi.Alias,
) {
	state := fmt.Sprintf("ownership:%s/%s:%s", actual.Namespace, actual.Service, actual.Owners)
	if !p.setAliasClaimState(service, key, state) {
		return
	}

	message := fmt.Sprintf("alias %s points to %s/%s with owner %q; refusing to update it",
		key.String(), actual.Namespace, actual.Service, actual.Owners)
	log.SyncNamingScope().Warnf("%s/%s: %s", service.GetNamespace(), service.GetName(), message)
	p.eventRecorder.Eventf(service, v1.EventTypeWarning, "AliasOwnershipConflict", "%s", message)
	metrics.AliasBindingConflicts.WithLabelValues(
		key.Namespace, key.Service, service.GetNamespace(), service.GetName(), "ownership").Inc()
}

func (p *PolarisController) setAliasClaimState(service *v1.Service, key aliasKey, state string) bool {
	stateKey := aliasClaimStateKey(service, key)
	if old, ok := p.aliasClaimStates.Load(stateKey); ok && old == state {
		return false
	}
	p.aliasClaimStates.Store(stateKey, state)
	return true
}

func (p *PolarisController) clearAliasClaimState(service *v1.Service, key aliasKey) {
	p.aliasClaimStates.Delete(aliasClaimStateKey(service, key))
}

func aliasClaimStateKey(service *v1.Service, key aliasKey) string {
	return key.String() + "|" + service.GetNamespace() + "/" + service.GetName() +
		"#" + string(service.GetUID())
}
