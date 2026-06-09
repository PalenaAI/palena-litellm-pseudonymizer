// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPools_AssignFromPool(t *testing.T) {
	p := NewPools(map[string][]string{
		"PERSON": {"Alice", "Bob", "Carol"},
	})
	used := map[string]struct{}{}
	got := p.Assign("PERSON", used)
	require.Equal(t, "Alice", got)
	used["Alice"] = struct{}{}
	require.Equal(t, "Bob", p.Assign("PERSON", used))
}

func TestPools_SyntheticFallbackWhenExhausted(t *testing.T) {
	p := NewPools(map[string][]string{"PERSON": {"Alice"}})
	used := map[string]struct{}{"Alice": {}}
	require.Equal(t, "Person-1", p.Assign("PERSON", used))
	used["Person-1"] = struct{}{}
	require.Equal(t, "Person-2", p.Assign("PERSON", used))
}

func TestPools_UnknownEntityTypeUsesTitleCase(t *testing.T) {
	p := NewPools(map[string][]string{})
	used := map[string]struct{}{}
	require.Equal(t, "Nrp-1", p.Assign("NRP", used))
}

func TestPools_WhitespaceEntriesStripped(t *testing.T) {
	p := NewPools(map[string][]string{"PERSON": {"  Alice  ", "", " Bob"}})
	used := map[string]struct{}{}
	require.Equal(t, "Alice", p.Assign("PERSON", used))
	used["Alice"] = struct{}{}
	require.Equal(t, "Bob", p.Assign("PERSON", used))
}

func TestPools_EmptyPoolExhaustsToSynthetic(t *testing.T) {
	p := NewPools(map[string][]string{})
	require.Equal(t, "Person-1", p.Assign("PERSON", map[string]struct{}{}))
}
