// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"fmt"
	"strings"
)

// Pools holds the list of fictional names available for each entity
// type. See context/PSEUDONYMIZATION_ALGORITHM.md § "Pool assignment"
// for the exact assignment algorithm.
type Pools struct {
	byType map[string][]string
}

// syntheticPrefix returns the fallback prefix used when a pool is
// exhausted. Unknown types fall back to a TitleCase of the type name.
var syntheticPrefix = map[string]string{
	"PERSON":       "Person",
	"ORGANIZATION": "Organization",
	"LOCATION":     "Location",
}

// NewPools constructs a Pools from a map[type]list of names.
func NewPools(pools map[string][]string) *Pools {
	byType := make(map[string][]string, len(pools))
	for k, v := range pools {
		cleaned := make([]string, 0, len(v))
		for _, entry := range v {
			e := strings.TrimSpace(entry)
			if e != "" {
				cleaned = append(cleaned, e)
			}
		}
		byType[k] = cleaned
	}
	return &Pools{byType: byType}
}

// Assign returns the next unused pseudonym for entityType, given the
// current mapping's values (any pseudonym already assigned).
//
// The chosen pseudonym is NOT added to `used` — callers add it via
// their own scratch map so multiple concurrent assignments in one
// request don't collide.
func (p *Pools) Assign(entityType string, used map[string]struct{}) string {
	for _, candidate := range p.byType[entityType] {
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
	// Pool exhausted — synthetic fallback. Prefix-N for the smallest
	// positive N that produces an unused name.
	prefix := syntheticPrefix[entityType]
	if prefix == "" {
		// Unknown entity type: TitleCase the type name once.
		prefix = titleCase(entityType)
	}
	for n := 1; ; n++ {
		candidate := fmt.Sprintf("%s-%d", prefix, n)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
}

// usedFromMap returns a set of values from a real→pseudo mapping.
// Helper for callers.
func usedFromMap(m map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for _, v := range m {
		out[v] = struct{}{}
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	lowered := strings.ToLower(s)
	return strings.ToUpper(lowered[:1]) + lowered[1:]
}
