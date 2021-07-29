package cache

import (
	"sync"

	v1 "k8s.io/api/core/v1"
)

// CachedServiceMap
func NewCachedServiceMap() *CachedServiceMap {
	return &CachedServiceMap{}
}

// CachedServiceMap key：string， value: *v1.service
type CachedServiceMap struct {
	sm sync.Map
}

// Delete
func (csm *CachedServiceMap) Delete(key string) {
	csm.sm.Delete(key)
}

// Load
func (csm *CachedServiceMap) Load(key string) (value *v1.Service, ok bool) {
	v, ok := csm.sm.Load(key)
	if v != nil {
		value, ok2 := v.(*v1.Service)
		if !ok2 {
			ok = false
		}
		return value, ok
	}
	return value, ok
}

// Store
func (csm *CachedServiceMap) Store(key string, value *v1.Service) {
	csm.sm.Store(key, value)
}
