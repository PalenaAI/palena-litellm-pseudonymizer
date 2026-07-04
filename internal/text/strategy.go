// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Strategy selects how a detected entity is substituted.
type Strategy string

const (
	// StrategyPool replaces the entity with a realistic fictional value
	// from a configured pool. Right for nominal identities the model
	// reasons about (PERSON, ORGANIZATION, LOCATION).
	StrategyPool Strategy = "pool"

	// StrategyToken replaces the entity with a consistent, reversible
	// placeholder like <CREDIT_CARD_1>. Right for structured PII
	// (CREDIT_CARD, US_SSN, IBAN_CODE, EMAIL_ADDRESS, custom IDs …) where a
	// fake realistic value is meaningless and the model rarely needs the
	// actual bytes.
	StrategyToken Strategy = "token"
)

// nominalTypes default to pool substitution unless overridden.
var nominalTypes = map[string]struct{}{
	"PERSON":       {},
	"ORGANIZATION": {},
	"LOCATION":     {},
}

// Strategizer assigns a pseudonym to a newly-detected entity, dispatching
// on the per-entity substitution strategy.
type Strategizer struct {
	pools    *Pools
	byEntity map[string]Strategy // explicit per-type overrides
	def      Strategy            // fallback for non-nominal, non-overridden types
}

// StrategizerConfig groups constructor parameters.
type StrategizerConfig struct {
	Pools     *Pools
	Overrides map[string]string // entityType -> "pool"|"token"
	Default   string            // "pool"|"token"; empty -> "token"
}

// NewStrategizer builds a Strategizer.
func NewStrategizer(cfg StrategizerConfig) *Strategizer {
	byEntity := make(map[string]Strategy, len(cfg.Overrides))
	for k, v := range cfg.Overrides {
		byEntity[k] = Strategy(v)
	}
	def := Strategy(cfg.Default)
	if def != StrategyPool && def != StrategyToken {
		def = StrategyToken
	}
	pools := cfg.Pools
	if pools == nil {
		pools = NewPools(nil)
	}
	return &Strategizer{pools: pools, byEntity: byEntity, def: def}
}

// StrategyFor resolves the strategy for an entity type: explicit override
// wins, then nominal types default to pool, then the configured default.
func (s *Strategizer) StrategyFor(entityType string) Strategy {
	if st, ok := s.byEntity[entityType]; ok {
		return st
	}
	if _, ok := nominalTypes[entityType]; ok {
		return StrategyPool
	}
	return s.def
}

// Assign returns a pseudonym for a newly-detected entity of entityType.
// scratch is the session's current real→pseudonym mapping plus any
// assignments made earlier in this request; it is used to avoid collisions
// (pool) and to compute the next token index (token).
func (s *Strategizer) Assign(entityType, real string, scratch map[string]string) string {
	if s.StrategyFor(entityType) == StrategyToken {
		return nextToken(entityType, scratch)
	}
	return s.pools.Assign(entityType, usedFromMap(scratch))
}

// tokenRE matches a token pseudonym like "<CREDIT_CARD_12>" and captures
// the entity-type prefix and the index.
var tokenRE = regexp.MustCompile(`^<([A-Z0-9_]+)_(\d+)>$`)

// tokenPrefix normalizes an entity type into the token prefix
// (uppercase, spaces/dashes -> underscore) so custom types render cleanly.
func tokenPrefix(entityType string) string {
	up := strings.ToUpper(strings.TrimSpace(entityType))
	up = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, up)
	up = strings.Trim(up, "_")
	if up == "" {
		up = "PII"
	}
	return up
}

// nextToken returns the next unused "<PREFIX_N>" token for entityType,
// scanning scratch for the current max index of this prefix. Tokens are
// unique by construction, so no collision check is needed.
func nextToken(entityType string, scratch map[string]string) string {
	prefix := tokenPrefix(entityType)
	maxN := 0
	for _, v := range scratch {
		m := tokenRE.FindStringSubmatch(v)
		if m == nil || m[1] != prefix {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("<%s_%d>", prefix, maxN+1)
}

// isToken reports whether s is one of our token pseudonyms. Used by the
// replacer to skip case-preservation when reversing tokens (a token is an
// exact stand-in; case-matching would corrupt values with lowercase
// letters, e.g. an email).
func isToken(s string) bool {
	return tokenRE.MatchString(s)
}
