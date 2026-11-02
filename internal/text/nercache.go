// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"time"
)

// NERCache remembers what the span model found in a line, so a conversation
// that is sent again every turn (Wäg's pre_call scan carries the whole
// history, and a second pass message by message) only costs model time for
// its new lines.
//
// Keys are SHA-256 digests of the line plus the labels and threshold asked
// for — the line itself is never kept. Values are the spans found (offsets,
// label, score). An entry lives TTL from when it was stored (reads do not
// extend it); at MaxEntries the least recently used entry makes room. Errors
// are never cached. Safe for concurrent use.
type NERCache struct {
	ttl time.Duration
	max int
	now func() time.Time

	mu    sync.Mutex
	order *list.List // front = most recently used
	items map[[sha256.Size]byte]*list.Element

	hits, misses, evictions atomic.Uint64
}

type nerCacheEntry struct {
	key     [sha256.Size]byte
	expires int64 // unix nanoseconds
	spans   []cachedSpan
}

// cachedSpan is one detection in character offsets of the cached line. The
// label points at the detector's own label string, not a copy.
type cachedSpan struct {
	start, end int32
	score      float64
	label      string
}

// NewNERCache returns a cache of at most maxEntries entries living ttl each,
// or nil (no caching) when either is not positive.
func NewNERCache(maxEntries int, ttl time.Duration) *NERCache {
	if maxEntries <= 0 || ttl <= 0 {
		return nil
	}
	return &NERCache{
		ttl:   ttl,
		max:   maxEntries,
		now:   time.Now,
		order: list.New(),
		items: make(map[[sha256.Size]byte]*list.Element),
	}
}

// NERCacheStats is a point-in-time view for metrics.
type NERCacheStats struct {
	Entries                 int
	Hits, Misses, Evictions uint64
}

// Stats reports the counters and the current size.
func (c *NERCache) Stats() NERCacheStats {
	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()
	return NERCacheStats{Entries: n, Hits: c.hits.Load(), Misses: c.misses.Load(), Evictions: c.evictions.Load()}
}

func (c *NERCache) get(key [sha256.Size]byte) ([]cachedSpan, bool) {
	now := c.now().UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	e := el.Value.(*nerCacheEntry)
	if now >= e.expires {
		c.order.Remove(el)
		delete(c.items, key)
		c.misses.Add(1)
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits.Add(1)
	return e.spans, true
}

func (c *NERCache) put(key [sha256.Size]byte, spans []cachedSpan) {
	expires := c.now().Add(c.ttl).UnixNano()
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*nerCacheEntry)
		e.spans, e.expires = spans, expires
		c.order.MoveToFront(el)
		return
	}
	for len(c.items) >= c.max {
		// Expired entries drift to the back too (they are never read), so
		// this also drops what has outlived its TTL.
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*nerCacheEntry).key)
		c.evictions.Add(1)
	}
	c.items[key] = c.order.PushFront(&nerCacheEntry{key: key, expires: expires, spans: spans})
}

// nerCacheKey digests a line together with what it was asked for.
func nerCacheKey(scope, line string) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(scope))
	h.Write([]byte{0})
	h.Write([]byte(line))
	var k [sha256.Size]byte
	h.Sum(k[:0])
	return k
}
