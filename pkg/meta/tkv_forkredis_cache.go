/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package meta

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const forkDefaultCacheBytes = 16 << 20
const forkReadBatchKeys = 128

// The owning client fixes the Redis database, volume, branch and ancestry.
// Mutable layers use the remotely pinned revision, never a time-based TTL.
type forkCacheKey struct {
	layer    string
	revision uint64
	kind     uint8 // 0: record (including absent/tombstone), 1: exact index range
	begin    string
	end      string
}

type forkCacheValue struct {
	data  []byte // nil means absent; a tombstone is a nonempty encoded record
	names []string
}

func (v forkCacheValue) cost(k forkCacheKey) int {
	// Include entry/list/map overhead and string/slice headers in the budget.
	n := 256 + len(k.layer) + len(k.begin) + len(k.end) + len(v.data)
	for _, name := range v.names {
		n += 16 + len(name)
	}
	return n
}

type forkCacheEntry struct {
	key   forkCacheKey
	value forkCacheValue
	cost  int
}

type forkReadCache struct {
	mu      sync.Mutex
	limit   int
	used    int
	entries map[forkCacheKey]*list.Element
	lru     list.List
	hits    [2]atomic.Uint64
	misses  [2]atomic.Uint64
}

func newForkReadCache(limit int) *forkReadCache {
	return &forkReadCache{limit: limit, entries: make(map[forkCacheKey]*list.Element)}
}

func (c *forkReadCache) get(key forkCacheKey) (forkCacheValue, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		c.hits[key.kind].Add(1)
		return e.Value.(forkCacheEntry).value, true
	}
	c.misses[key.kind].Add(1)
	return forkCacheValue{}, false
}

// Values are immutable and remain private to the driver. Callers receive copies
// when decoding records, so mutation cannot change an entry or another snapshot.
func (c *forkReadCache) put(key forkCacheKey, value forkCacheValue) {
	cost := value.cost(key)
	if cost > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return
	}
	for c.used+cost > c.limit {
		e := c.lru.Back()
		entry := e.Value.(forkCacheEntry)
		delete(c.entries, entry.key)
		c.used -= entry.cost
		c.lru.Remove(e)
	}
	c.entries[key] = c.lru.PushFront(forkCacheEntry{key, value, cost})
	c.used += cost
}

func (tx *forkRedisTxn) cached(key forkCacheKey) (forkCacheValue, bool) {
	if value, ok := tx.pending[key]; ok {
		tx.c.cache.hits[key.kind].Add(1)
		return value, true
	}
	return tx.c.cache.get(key)
}

func (tx *forkRedisTxn) remember(key forkCacheKey, value forkCacheValue) {
	if _, ok := tx.pending[key]; ok {
		return
	}
	cost := value.cost(key)
	// Also bound uncommitted cache entries in each transaction. Over-budget
	// results still work, but are not admitted to the cache.
	if tx.pendingBytes+cost > tx.c.cache.limit {
		return
	}
	if tx.pending == nil {
		tx.pending = make(map[forkCacheKey]forkCacheValue)
	}
	tx.pending[key] = value
	tx.pendingBytes += cost
}

func (tx *forkRedisTxn) publishReads() {
	defer tx.c.phases[forkPhasePublish].observe(time.Now())
	// Called only after remote snapshot/commit validation. An expired or failed
	// reader must not publish a false absence after its old versions were pruned.
	for key, value := range tx.pending {
		tx.c.cache.put(key, value)
	}
}

func (c *forkRedisClient) initMetrics(reg prometheus.Registerer) {
	c.initTimingMetrics(reg)
	for kind, name := range []string{"record", "range"} {
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "forkredis_cache_hits_total", Help: "Versioned read cache hits.",
			ConstLabels: prometheus.Labels{"kind": name},
		}, func() float64 { return float64(c.cache.hits[kind].Load()) }))
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "forkredis_cache_misses_total", Help: "Versioned read cache misses.",
			ConstLabels: prometheus.Labels{"kind": name},
		}, func() float64 { return float64(c.cache.misses[kind].Load()) }))
	}
	reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "forkredis_read_batches_total", Help: "Redis read pipeline executions, excluding transaction validation.",
	}, func() float64 { return float64(c.readBatches.Load()) }))
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "forkredis_cache_bytes", Help: "Versioned cache accounted resident bytes (including entry overhead).",
	}, func() float64 { c.cache.mu.Lock(); defer c.cache.mu.Unlock(); return float64(c.cache.used) }))
}
