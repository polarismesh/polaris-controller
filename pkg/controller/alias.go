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

	actual, found, err := polarisapi.GetServiceAlias(key.Namespace, key.Service)
	if err != nil {
		return fmt.Errorf("get alias %s: %w", key.String(), err)
	}
	if !found {
		if _, err = polarisapi.CreateServiceAlias(service, p.aliasOwner(service)); err != nil {
			return fmt.Errorf("create alias %s: %w", key.String(), err)
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

	if _, err = polarisapi.UpdateServiceAlias(service, actual, p.aliasOwner(service)); err != nil {
		return fmt.Errorf("update alias %s: %w", key.String(), err)
	}
	if err = p.verifyServiceAlias(service, key); err != nil {
		return err
	}
	p.clearAliasClaimState(service, key)
	return nil
}

func (p *PolarisController) aliasOwner(service *v1.Service) string {
	return fmt.Sprintf("%s/%s/%s", polarisapi.Source, p.config.PolarisController.ClusterName, service.GetUID())
}

func (p *PolarisController) canUpdateAlias(service *v1.Service, actual polarisapi.Alias) bool {
	if actual.Owners == polarisapi.Source || actual.Owners == p.aliasOwner(service) {
		return true
	}
	return strings.HasPrefix(actual.Owners,
		fmt.Sprintf("%s/%s/", polarisapi.Source, p.config.PolarisController.ClusterName))
}

func (p *PolarisController) verifyServiceAlias(service *v1.Service, key aliasKey) error {
	actual, found, err := polarisapi.GetServiceAlias(key.Namespace, key.Service)
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
