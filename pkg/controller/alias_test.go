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
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/polarismesh/polaris-controller/pkg/polarisapi"
	"github.com/polarismesh/polaris-controller/pkg/util"
)

type aliasAPITestState struct {
	mu      sync.Mutex
	aliases map[string]polarisapi.Alias
}

func newAliasAPITestState() *aliasAPITestState {
	return &aliasAPITestState{aliases: make(map[string]polarisapi.Alias)}
}

func (s *aliasAPITestState) responder(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.URL.Path {
	case "/naming/v1/namespaces":
		return aliasJSONResponse(http.StatusOK, `{"code":200000,"info":"success","responses":[{"code":200000}]}`)
	case "/naming/v1/service/aliases":
		key := aliasTestKey(r.URL.Query().Get("alias_namespace"), r.URL.Query().Get("alias"))
		response := polarisapi.GetServiceAliasesResponse{Code: polarisapi.ExecuteSuccess, Info: "success"}
		if alias, ok := s.aliases[key]; ok {
			response.Aliases = append(response.Aliases, alias)
			response.Amount = 1
			response.Size = 1
		}
		body, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return aliasJSONResponse(http.StatusOK, string(body))
	case "/naming/v1/service/alias":
		var request polarisapi.CreateServiceAliasRequest
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		key := aliasTestKey(request.AliasNamespace, request.Alias)
		alias, exists := s.aliases[key]
		if r.Method == http.MethodPost && exists {
			return aliasJSONResponse(http.StatusBadRequest,
				`{"code":400201,"info":"resource exists"}`)
		}
		if r.Method == http.MethodPut && !exists {
			return aliasJSONResponse(http.StatusBadRequest,
				`{"code":400304,"info":"alias not found"}`)
		}

		alias = polarisapi.Alias{
			Service:        request.Service,
			Namespace:      request.Namespace,
			Alias:          request.Alias,
			AliasNamespace: request.AliasNamespace,
			Owners:         request.Owners,
			Comment:        request.Comment,
		}
		s.aliases[key] = alias
		response := polarisapi.CreateServiceAliasResponse{
			Code:  polarisapi.ExecuteSuccess,
			Info:  "success",
			Alias: alias,
		}
		responseBody, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}
		return aliasJSONResponse(http.StatusOK, string(responseBody))
	default:
		return aliasJSONResponse(http.StatusInternalServerError,
			`{"code":500,"info":"unexpected alias API request"}`)
	}
}

func aliasJSONResponse(status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func aliasTestKey(namespace, alias string) string {
	return namespace + "/" + alias
}

func aliasTestService(name, uid string, createdAt time.Time) *v1.Service {
	service := testService()
	service.Name = name
	service.UID = types.UID(uid)
	service.CreationTimestamp = metav1.NewTime(createdAt)
	service.Annotations[util.PolarisAliasNamespace] = "Production"
	service.Annotations[util.PolarisAliasService] = "order"
	return service
}

func TestReconcileServiceAliasCreatesMissingAlias(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}

	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order" || actual.Namespace != "ns" ||
		actual.Owners != "polaris-controller/test/uid-order" {
		t.Fatalf("unexpected alias: %+v", actual)
	}
	if got := requestPaths(tr.requests); !equalStrings(got, []string{
		"/naming/v1/service/aliases",
		"/naming/v1/namespaces",
		"/naming/v1/service/alias",
		"/naming/v1/service/aliases",
	}) {
		t.Fatalf("unexpected requests: %v", got)
	}
}

func TestReconcileServiceAliasUpdatesWrongSource(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order-old",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	service := aliasTestService("order-new", "uid-order-new", time.Unix(200, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}

	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order-new" {
		t.Fatalf("alias points to %q, want order-new", actual.Service)
	}
	if got := requestPaths(tr.requests); !equalStrings(got, []string{
		"/naming/v1/service/aliases",
		"/naming/v1/service/alias",
		"/naming/v1/service/aliases",
	}) {
		t.Fatalf("unexpected requests: %v", got)
	}
}

func TestReconcileServiceAliasNoOpWhenBindingIsCorrect(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}
	if got := requestPaths(tr.requests); !equalStrings(got, []string{"/naming/v1/service/aliases"}) {
		t.Fatalf("correct alias caused writes: %v", got)
	}
}

func TestReconcileServiceAliasUsesEarliestClaim(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order-new",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	first := aliasTestService("order-old", "uid-old", time.Unix(100, 0))
	second := aliasTestService("order-new", "uid-new", time.Unix(200, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, second)

	if err := p.reconcileServiceAlias(second); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("non-winner wrote alias: %v", requestPaths(tr.requests))
	}

	if err := p.reconcileServiceAlias(first); err != nil {
		t.Fatal(err)
	}
	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order-old" {
		t.Fatalf("alias points to %q, want order-old", actual.Service)
	}
}

func TestReconcileServiceAliasSerializesConcurrentClaims(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order-new",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	first := aliasTestService("order-old", "uid-old", time.Unix(100, 0))
	second := aliasTestService("order-new", "uid-new", time.Unix(200, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, second)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, service := range []*v1.Service{first, second} {
		wg.Add(1)
		go func(service *v1.Service) {
			defer wg.Done()
			errs <- p.reconcileServiceAlias(service)
		}(service)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order-old" {
		t.Fatalf("alias points to %q, want order-old", actual.Service)
	}
}

func TestReconcileServiceAliasAllowsNextOwnerAfterWinnerStopsClaiming(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order-old",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	first := aliasTestService("order-old", "uid-old", time.Unix(100, 0))
	second := aliasTestService("order-new", "uid-new", time.Unix(200, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, second)

	firstWithoutAlias := first.DeepCopy()
	delete(firstWithoutAlias.Annotations, util.PolarisAliasNamespace)
	delete(firstWithoutAlias.Annotations, util.PolarisAliasService)
	if err := stores.services.Update(firstWithoutAlias); err != nil {
		t.Fatal(err)
	}

	if err := p.reconcileServiceAlias(second); err != nil {
		t.Fatal(err)
	}
	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order-new" {
		t.Fatalf("alias points to %q, want order-new", actual.Service)
	}
}

func TestReconcileServiceAliasUsesUIDAsCreationTimeTieBreaker(t *testing.T) {
	p, stores := newTestController(t)
	mockPolarisHTTP(t)

	first := aliasTestService("order-b", "uid-a", time.Unix(100, 0))
	second := aliasTestService("order-a", "uid-b", time.Unix(100, 0))
	storeObject(t, stores.services, first)
	storeObject(t, stores.services, second)

	claims, err := p.aliasClaims(aliasKey{Namespace: "Production", Service: "order"}, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].UID != "uid-a" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestReconcileServiceAliasDoesNotOverwriteManualAlias(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "manual-service",
		Namespace:      "manual",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         "manual-owner",
	}
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}

	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "manual-service" || actual.Owners != "manual-owner" {
		t.Fatalf("manual alias was changed: %+v", actual)
	}
	if got := requestPaths(tr.requests); !equalStrings(got, []string{"/naming/v1/service/aliases"}) {
		t.Fatalf("unexpected requests: %v", got)
	}
}

func TestReconcileServiceAliasDoesNotOverwriteOtherClusterOwner(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order-other-cluster",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         "polaris-controller/other-cluster/uid-other",
	}
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}
	actual := state.aliases[aliasTestKey("Production", "order")]
	if actual.Service != "order-other-cluster" {
		t.Fatalf("other cluster alias was changed: %+v", actual)
	}
}

func TestReconcileServiceAliasReturnsQueryError(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(r *http.Request) (*http.Response, error) {
		return aliasJSONResponse(http.StatusInternalServerError,
			`{"code":500,"info":"alias query failed"}`)
	}

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err == nil {
		t.Fatal("alias query failure did not return an error")
	}
}

func TestProcessUpdateServiceReturnsAliasError(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	tr.respond = func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/naming/v1/service/aliases" {
			return aliasJSONResponse(http.StatusInternalServerError,
				`{"code":500,"info":"alias query failed"}`)
		}
		return successResponse(), nil
	}

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	storeObject(t, stores.services, service)
	addTestEndpoint(t, stores, "10.0.0.1")
	if err := p.processUpdateService(service, service); err == nil {
		t.Fatal("processUpdateService hid alias error")
	}
}

func TestReconcileServiceAliasIgnoresRemovedAnnotations(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	service := testService()
	service.Name = "order"
	service.UID = types.UID("uid-order")
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("removed alias annotations caused writes: %v", requestPaths(tr.requests))
	}
	if _, ok := state.aliases[aliasTestKey("Production", "order")]; !ok {
		t.Fatal("alias was deleted")
	}
}

func TestReconcileServiceAliasKeepsOldAliasAfterRename(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order-old")] = polarisapi.Alias{
		Service:        "order",
		Namespace:      "ns",
		Alias:          "order-old",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	service.Annotations[util.PolarisAliasService] = "order-new"
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.aliases[aliasTestKey("Production", "order-old")]; !ok {
		t.Fatal("old alias was deleted")
	}
	actual := state.aliases[aliasTestKey("Production", "order-new")]
	if actual.Service != "order" || actual.Namespace != "ns" {
		t.Fatalf("new alias not bound: %+v", actual)
	}
}

func TestReconcileServiceAliasKeepsAliasWhenServiceStopsSync(t *testing.T) {
	p, stores := newTestController(t)
	tr := mockPolarisHTTP(t)
	state := newAliasAPITestState()
	state.aliases[aliasTestKey("Production", "order")] = polarisapi.Alias{
		Service:        "order",
		Namespace:      "ns",
		Alias:          "order",
		AliasNamespace: "Production",
		Owners:         polarisapi.Source,
	}
	tr.respond = state.responder

	service := aliasTestService("order", "uid-order", time.Unix(100, 0))
	service.Annotations[util.PolarisSync] = "false"
	storeObject(t, stores.services, service)
	if err := p.reconcileServiceAlias(service); err != nil {
		t.Fatal(err)
	}
	if len(tr.requests) != 0 {
		t.Fatalf("stopped service caused alias writes: %v", requestPaths(tr.requests))
	}
	if _, ok := state.aliases[aliasTestKey("Production", "order")]; !ok {
		t.Fatal("alias was deleted")
	}
}

func requestPaths(requests []capturedRequest) []string {
	paths := make([]string, 0, len(requests))
	for _, request := range requests {
		paths = append(paths, request.path)
	}
	return paths
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
