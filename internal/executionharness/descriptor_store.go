package executionharness

import (
	"context"
	"encoding/json"
	"sync"
)

// MemoryRunDescriptorStore is the deterministic descriptor adapter used by
// unit tests and by callers that deliberately run an in-process Harness. A
// production bootstrap should provide the PostgreSQL adapter; this store is
// process-local and therefore does not claim restart durability.
type MemoryRunDescriptorStore struct {
	mu          sync.Mutex
	descriptors map[runDescriptorKey]memoryRunDescriptor
}

type runDescriptorKey struct {
	organizationID string
	runID          string
}

type memoryRunDescriptor struct {
	descriptor RunDescriptor
	digest     string
}

func NewMemoryRunDescriptorStore() *MemoryRunDescriptorStore {
	return &MemoryRunDescriptorStore{descriptors: make(map[runDescriptorKey]memoryRunDescriptor)}
}

func (s *MemoryRunDescriptorStore) EnsureRunDescriptor(ctx context.Context, descriptor RunDescriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := descriptor.CanonicalBytes()
	if err != nil {
		return err
	}
	var normalized RunDescriptor
	if err = json.Unmarshal(body, &normalized); err != nil {
		return ErrRunDescriptorCorrupt
	}
	digest, err := normalized.CanonicalDigest()
	if err != nil {
		return err
	}
	key := runDescriptorKey{organizationID: normalized.OrganizationID, runID: normalized.RunID}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.descriptors[key]; ok {
		if existing.digest != digest {
			return ErrRunDescriptorConflict
		}
		return nil
	}
	s.descriptors[key] = memoryRunDescriptor{descriptor: normalized, digest: digest}
	return nil
}

func (s *MemoryRunDescriptorStore) ReadRunDescriptor(ctx context.Context, organizationID, runID string) (RunDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return RunDescriptor{}, err
	}
	if organizationID == "" || runID == "" {
		return RunDescriptor{}, ErrRunDescriptorNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.descriptors[runDescriptorKey{organizationID: organizationID, runID: runID}]
	if !ok {
		return RunDescriptor{}, ErrRunDescriptorNotFound
	}
	digest, err := existing.descriptor.CanonicalDigest()
	if err != nil || digest != existing.digest {
		return RunDescriptor{}, ErrRunDescriptorCorrupt
	}
	return cloneRunDescriptor(existing.descriptor), nil
}

func cloneRunDescriptor(descriptor RunDescriptor) RunDescriptor {
	descriptor.FrozenTools = append([]FrozenToolRef(nil), descriptor.FrozenTools...)
	return descriptor
}

var _ RunDescriptorStore = (*MemoryRunDescriptorStore)(nil)
