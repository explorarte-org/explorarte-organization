package search

import (
	"sync"
	"time"
)

type CacheEntry struct {
	Results  []SearchResult
	StoredAt time.Time
	Ttl      time.Duration
}

type Cache interface {
	Get(req SearchRequest, now time.Time) (CacheEntry, bool)
	Put(key, req SearchRequest, entry CacheEntry)
	Reap(now time.Time, limit int) (int, error)
}

type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]*cachedEntry
}

type cachedEntry struct {
	req       SearchRequest
	entry     CacheEntry
	expiresAt time.Time
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{entries: make(map[string]*cachedEntry)}
}

func (c *MemoryCache) Get(req SearchRequest, now time.Time) (CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := cacheKey(req)
	if e, ok := c.entries[key]; ok {
		if now.Before(e.expiresAt) {
			return e.entry, true
		}
	}
	return CacheEntry{}, false
}

func (c *MemoryCache) Put(key, req SearchRequest, entry CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := cacheKey(key)
	c.entries[k] = &cachedEntry{
		req:       req,
		entry:     entry,
		expiresAt: time.Now().UTC().Add(entry.Ttl),
	}
}

func (c *MemoryCache) Reap(now time.Time, limit int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reaped := 0
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
			reaped++
			if reaped >= limit {
				break
			}
		}
	}
	return reaped, nil
}

func cacheKey(req SearchRequest) string {
	return string(req.Intent) + "|" + req.Query + "|" + req.RoleID
}

var DefaultTTLs = map[SearchIntent]time.Duration{
	IntentNews:              15 * time.Minute,
	IntentWebGeneral:        6 * time.Hour,
	IntentAcademic:          24 * time.Hour,
	IntentBiomedical:        24 * time.Hour,
	IntentPreprint:          12 * time.Hour,
	IntentBookGeneral:       7 * 24 * time.Hour,
	IntentBookAcademicOA:    30 * 24 * time.Hour,
	IntentBookPublicDomain:  30 * 24 * time.Hour,
	IntentDOIResolve:        30 * 24 * time.Hour,
	IntentOpenAccessResolve: 30 * 24 * time.Hour,
}
