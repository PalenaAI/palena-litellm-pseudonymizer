// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewNERCache_ZeroDisables(t *testing.T) {
	require.Nil(t, NewNERCache(0, time.Hour))
	require.Nil(t, NewNERCache(10, 0))
}

func TestNERCache_EntriesExpireAfterTTL(t *testing.T) {
	c := NewNERCache(10, time.Hour)
	now := time.Unix(1_000, 0)
	c.now = func() time.Time { return now }
	k := nerCacheKey("person|0.9", "Sabine Keller signed.")
	c.put(k, []cachedSpan{{start: 0, end: 13, score: 0.95, label: "person"}})

	now = now.Add(59 * time.Minute)
	got, ok := c.get(k)
	require.True(t, ok)
	require.Equal(t, int32(13), got[0].end)

	// A read does not extend the entry's life.
	now = now.Add(time.Minute)
	_, ok = c.get(k)
	require.False(t, ok, "expired one hour after it was stored")
	require.Equal(t, NERCacheStats{Entries: 0, Hits: 1, Misses: 1}, c.Stats(), "an expired entry is removed when found")
}

func TestNERCache_FullCacheDropsLeastRecentlyUsed(t *testing.T) {
	c := NewNERCache(3, time.Hour)
	key := func(i int) [32]byte { return nerCacheKey("s", fmt.Sprint(i)) }
	for i := 1; i <= 3; i++ {
		c.put(key(i), nil)
	}
	_, ok := c.get(key(1)) // 1 is now the most recently used; 2 the least
	require.True(t, ok)
	c.put(key(4), nil)

	_, ok = c.get(key(2))
	require.False(t, ok, "least recently used entry made room")
	for _, i := range []int{1, 3, 4} {
		_, ok := c.get(key(i))
		require.True(t, ok, i)
	}
	st := c.Stats()
	require.Equal(t, 3, st.Entries, "never above the cap")
	require.Equal(t, uint64(1), st.Evictions)
}

func TestNERCache_KeyCoversWhatWasAsked(t *testing.T) {
	require.NotEqual(t, nerCacheKey("person|0.9", "x"), nerCacheKey("person|0.8", "x"), "threshold")
	require.NotEqual(t, nerCacheKey("person|0.9", "x"), nerCacheKey("full_name|person|0.9", "x"), "labels")
	require.NotEqual(t, nerCacheKey("a", "bc"), nerCacheKey("ab", "c"), "scope and line are separated")
}

func cachedDetector(stub *stubNER) *NERDetector {
	return &NERDetector{
		NER: stub, Labels: map[string]string{"person": "PERSON"}, Threshold: 0.9,
		ChunkChars: 1000, ChunkOverlap: 150, Cache: NewNERCache(100, time.Hour),
	}
}

func TestNERDetector_CacheSendsOnlyNewLines(t *testing.T) {
	stub := &stubNER{names: map[string]string{"Sabine Keller": "person", "Kovács Péter": "person"}}
	d := cachedDetector(stub)
	turn1 := "Please loop in Sabine Keller.\nSure, I will."
	got, err := d.Detect(context.Background(), turn1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 1, stub.calls)

	// Next turn: the history again plus one new line — only that line is sent.
	turn2 := turn1 + "\nAnd tell Kovács Péter too."
	got, err = d.Detect(context.Background(), turn2)
	require.NoError(t, err)
	require.Equal(t, []string{"And tell Kovács Péter too."}, stub.texts)
	var names []string
	for _, det := range got {
		require.Equal(t, det.Text, turn2[det.Start:det.End], "cached spans map to the right bytes")
		names = append(names, det.Text)
	}
	require.Equal(t, []string{"Sabine Keller", "Kovács Péter"}, names)

	// Everything seen: no call at all.
	_, err = d.Detect(context.Background(), turn2)
	require.NoError(t, err)
	require.Equal(t, 2, stub.calls)
}

func TestNERDetector_RepeatedLineIsSentOnce(t *testing.T) {
	stub := &stubNER{names: map[string]string{"Sabine Keller": "person"}}
	got, err := cachedDetector(stub).Detect(context.Background(), "Sabine Keller\nok\nSabine Keller")
	require.NoError(t, err)
	require.Equal(t, []string{"Sabine Keller", "ok"}, stub.texts)
	require.Len(t, got, 2, "both occurrences are reported")
}

func TestNERDetector_FailuresAreNotCached(t *testing.T) {
	stub := &stubNER{names: map[string]string{"Sabine Keller": "person"}, err: errors.New("down")}
	d := cachedDetector(stub)
	_, err := d.Detect(context.Background(), "Sabine Keller")
	require.Error(t, err)

	stub.err = nil
	got, err := d.Detect(context.Background(), "Sabine Keller")
	require.NoError(t, err)
	require.Len(t, got, 1, "asked again after the failure")
	require.Equal(t, 2, stub.calls)
}
