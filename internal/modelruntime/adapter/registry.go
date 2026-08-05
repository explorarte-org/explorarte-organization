package adapter

import (
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	values map[string]modelruntime.ProviderAdapter
}

func NewRegistry(adapters ...modelruntime.ProviderAdapter) *Registry {
	r := &Registry{values: map[string]modelruntime.ProviderAdapter{}}
	for _, a := range adapters {
		if a != nil {
			r.values[a.ProviderID()] = a
		}
	}
	return r
}
func (r *Registry) Get(id string) (modelruntime.ProviderAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[id]
	return v, ok
}
