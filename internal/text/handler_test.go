// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/mapping"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

type stubAnalyzer struct {
	detections []presidio.Detection
	err        error
	calls      int
	languages  []string
}

func (s *stubAnalyzer) Analyze(_ context.Context, _ string, _ []string, language string) ([]presidio.Detection, error) {
	s.calls++
	s.languages = append(s.languages, language)
	return s.detections, s.err
}

func newTestHandler(t *testing.T, stub *stubAnalyzer) (*Handler, mapping.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := mapping.NewRedisStoreFromClient(client, mapping.Config{
		SessionTTL: time.Minute, Timeout: time.Second,
	}, nil)
	h := NewHandler(HandlerConfig{
		Analyzer: stub,
		Store:    store,
		Pools:    NewPools(map[string][]string{"PERSON": {"Alpha", "Beta"}, "ORGANIZATION": {"OrgOne", "OrgTwo"}}),
		Entities: []string{"PERSON", "ORGANIZATION"},
	})
	return h, store
}

// newDecomposingHandler uses two-token pool names so component
// decomposition has something to align against.
func newDecomposingHandler(t *testing.T, stub *stubAnalyzer) (*Handler, mapping.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := mapping.NewRedisStoreFromClient(client, mapping.Config{
		SessionTTL: time.Minute, Timeout: time.Second,
	}, nil)
	h := NewHandler(HandlerConfig{
		Analyzer:             stub,
		Store:                store,
		Pools:                NewPools(map[string][]string{"PERSON": {"Jordan Avery", "Taylor Morgan"}}),
		Entities:             []string{"PERSON"},
		DecomposePersonNames: true,
	})
	return h, store
}

// newStructuredHandler enables token substitution for structured PII.
func newStructuredHandler(t *testing.T, stub *stubAnalyzer) (*Handler, mapping.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := mapping.NewRedisStoreFromClient(client, mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	strat := NewStrategizer(StrategizerConfig{
		Pools:   NewPools(map[string][]string{"PERSON": {"Jordan Avery"}}),
		Default: "token",
	})
	h := NewHandler(HandlerConfig{
		Analyzer:    stub,
		Store:       store,
		Strategizer: strat,
		Entities:    []string{"PERSON", "CREDIT_CARD", "US_SSN"},
	})
	return h, store
}

func TestHandler_StructuredPII_TokenizedAndReversed(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Text: "Alice Johnson", Score: 0.9},
		{EntityType: "CREDIT_CARD", Text: "4111111111111111", Score: 1.0},
		{EntityType: "US_SSN", Text: "078-05-1120", Score: 0.95},
	}}
	h, _ := newStructuredHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(),
		"Charge Alice Johnson card 4111111111111111 SSN 078-05-1120.", "s1")
	require.NoError(t, err)
	// Person -> pool name; structured PII -> tokens.
	require.Equal(t, "Charge Jordan Avery card <CREDIT_CARD_1> SSN <US_SSN_1>.", res.Text)

	// Reversal restores all of them verbatim.
	got, err := h.Reverse(context.Background(),
		"Confirmed <CREDIT_CARD_1> for Jordan Avery, SSN <US_SSN_1>.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Confirmed 4111111111111111 for Alice Johnson, SSN 078-05-1120.", got)
}

// newAllowListHandler enables an allow-list of never-pseudonymized terms.
func newAllowListHandler(t *testing.T, stub *stubAnalyzer, allow map[string]struct{}) (*Handler, mapping.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := mapping.NewRedisStoreFromClient(client, mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	h := NewHandler(HandlerConfig{
		Analyzer:  stub,
		Store:     store,
		Pools:     NewPools(map[string][]string{"ORGANIZATION": {"Acme Corp"}}),
		Entities:  []string{"ORGANIZATION"},
		AllowList: allow,
	})
	return h, store
}

func TestHandler_AllowList_NotPseudonymized(t *testing.T) {
	// "SSN" is a common word the NER over-tags as ORGANIZATION; allow-list it.
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "ORGANIZATION", Text: "Northwind Labs", Score: 0.9},
		{EntityType: "ORGANIZATION", Text: "SSN", Score: 0.6},
	}}
	h, _ := newAllowListHandler(t, stub, map[string]struct{}{"ssn": {}})
	res, err := h.Pseudonymize(context.Background(), "The SSN for Northwind Labs is on file.", "s1")
	require.NoError(t, err)
	// Northwind Labs is pooled; the allow-listed "SSN" stays verbatim.
	require.Equal(t, "The SSN for Acme Corp is on file.", res.Text)
	require.Equal(t, 1, res.EntitiesDetected)
}

// A user who literally types a token like "<CREDIT_CARD_1>" must not collide
// with a real value we tokenize in the same text — collision hardening.
func TestHandler_CollisionReservedTokenAvoided(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "CREDIT_CARD", Text: "4111111111111111", Score: 1.0},
	}}
	h, _ := newStructuredHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(),
		"See ref <CREDIT_CARD_1>; charge card 4111111111111111.", "s1")
	require.NoError(t, err)
	// Real card must skip the reserved index 1 and become _2.
	require.Equal(t, "See ref <CREDIT_CARD_1>; charge card <CREDIT_CARD_2>.", res.Text)
}

func TestHandler_HigherScoreClassificationWins(t *testing.T) {
	// Same span reported as both CREDIT_CARD (1.0) and ORGANIZATION (0.85);
	// the credit-card classification must win so it tokenizes, not pools.
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "ORGANIZATION", Text: "4111111111111111", Score: 0.85},
		{EntityType: "CREDIT_CARD", Text: "4111111111111111", Score: 1.0},
	}}
	h, _ := newStructuredHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "Card 4111111111111111.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Card <CREDIT_CARD_1>.", res.Text)
}

func TestHandler_Decompose_FullThenBareFirstNameReverses(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Text: "Alice Johnson", Score: 0.9},
	}}
	h, _ := newDecomposingHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "Please book Alice Johnson.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Please book Jordan Avery.", res.Text)

	// Model shortens to just the first name → must reverse to "Alice".
	got, err := h.Reverse(context.Background(), "Jordan will travel.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Alice will travel.", got)

	// And the full pseudonym still reverses to the full real name.
	got, err = h.Reverse(context.Background(), "Jordan Avery confirmed.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Alice Johnson confirmed.", got)
}

func TestHandler_Decompose_BareFirstNameLaterTurnStaysConsistent(t *testing.T) {
	// Turn 1: full name introduces the mapping (+ components).
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Text: "Alice Johnson", Score: 0.9},
	}}
	h, _ := newDecomposingHandler(t, stub)
	_, err := h.Pseudonymize(context.Background(), "Alice Johnson is the client.", "s1")
	require.NoError(t, err)

	// Turn 2: a later message references just "Alice". Presidio detects
	// it as PERSON; it must reuse "Jordan", NOT get a fresh pseudonym.
	stub.detections = []presidio.Detection{{EntityType: "PERSON", Text: "Alice", Score: 0.9}}
	res, err := h.Pseudonymize(context.Background(), "Alice prefers mornings.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Jordan prefers mornings.", res.Text)
	require.Equal(t, 0, res.NewMappingsCreated, "bare first name reuses the component mapping")
}

func TestHandler_Decompose_Disabled_NoComponentMappings(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Text: "Alice Johnson", Score: 0.9},
	}}
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := mapping.NewRedisStoreFromClient(client, mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	h := NewHandler(HandlerConfig{
		Analyzer:             stub,
		Store:                store,
		Pools:                NewPools(map[string][]string{"PERSON": {"Jordan Avery"}}),
		Entities:             []string{"PERSON"},
		DecomposePersonNames: false,
	})
	_, err := h.Pseudonymize(context.Background(), "Alice Johnson is here.", "s1")
	require.NoError(t, err)
	// Bare first name does not reverse when decomposition is off.
	got, err := h.Reverse(context.Background(), "Jordan is here.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Jordan is here.", got)
}

func TestHandler_Pseudonymize_HappyPath(t *testing.T) {
	stub := &stubAnalyzer{
		detections: []presidio.Detection{
			{EntityType: "ORGANIZATION", Text: "Novartis", Score: 0.9},
			{EntityType: "PERSON", Text: "Thomas Weber", Score: 0.85},
		},
	}
	h, _ := newTestHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "Novartis hired Thomas Weber.", "s1")
	require.NoError(t, err)
	require.Equal(t, "OrgOne hired Alpha.", res.Text)
	require.Equal(t, 2, res.EntitiesDetected)
	require.Equal(t, 2, res.NewMappingsCreated)
	require.Equal(t, 2, res.SessionMappingSize)
}

func TestHandler_Pseudonymize_ExistingMappingReused(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "ORGANIZATION", Text: "Novartis", Score: 0.9},
	}}
	h, store := newTestHandler(t, stub)
	_, err := store.AddMappings(context.Background(), "s1", map[string]string{"Novartis": "PreAssigned"})
	require.NoError(t, err)

	res, err := h.Pseudonymize(context.Background(), "Novartis reports profit.", "s1")
	require.NoError(t, err)
	require.Equal(t, "PreAssigned reports profit.", res.Text)
	require.Equal(t, 0, res.NewMappingsCreated)
}

func TestHandler_Pseudonymize_NoEntities(t *testing.T) {
	stub := &stubAnalyzer{}
	h, _ := newTestHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "a quiet Sunday", "s1")
	require.NoError(t, err)
	require.Equal(t, "a quiet Sunday", res.Text)
	require.Equal(t, 0, res.EntitiesDetected)
}

func TestHandler_Pseudonymize_EmptyText(t *testing.T) {
	stub := &stubAnalyzer{}
	h, _ := newTestHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "", "s1")
	require.NoError(t, err)
	require.Equal(t, "", res.Text)
	require.Equal(t, 0, stub.calls, "empty input must skip Presidio")
}

func TestHandler_Pseudonymize_AnalyzerFailurePropagates(t *testing.T) {
	stub := &stubAnalyzer{err: errors.New("boom")}
	h, _ := newTestHandler(t, stub)
	_, err := h.Pseudonymize(context.Background(), "hello", "s1")
	require.Error(t, err)
}

func TestHandler_Reverse(t *testing.T) {
	stub := &stubAnalyzer{}
	h, store := newTestHandler(t, stub)
	_, err := store.AddMappings(context.Background(), "s1", map[string]string{"Novartis": "Alpha"})
	require.NoError(t, err)

	got, err := h.Reverse(context.Background(), "Alpha will hire.", "s1")
	require.NoError(t, err)
	require.Equal(t, "Novartis will hire.", got)
}

func TestHandler_Reverse_NoMappingIsPassthrough(t *testing.T) {
	stub := &stubAnalyzer{}
	h, _ := newTestHandler(t, stub)
	got, err := h.Reverse(context.Background(), "nothing to change", "s1")
	require.NoError(t, err)
	require.Equal(t, "nothing to change", got)
}

func TestHandler_LongestFirstAssignment(t *testing.T) {
	// Both "John" and "John Mueller" arrive from Presidio in the same call.
	// Longest first ensures John Mueller is assigned before John, so they
	// don't share a pseudonym.
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Text: "John", Score: 0.9},
		{EntityType: "PERSON", Text: "John Mueller", Score: 0.9},
	}}
	h, _ := newTestHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "John Mueller and John.", "s1")
	require.NoError(t, err)
	require.Equal(t, 2, res.NewMappingsCreated)
	// Alpha and Beta both assigned; either order OK, key point is
	// they differ and John Mueller resolves first.
	require.Contains(t, res.Text, "and ")
	require.NotEqual(t, "Alpha and Alpha.", res.Text)
}

func TestHandler_MultiTurnIdempotent(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "ORGANIZATION", Text: "Novartis", Score: 0.9},
	}}
	h, _ := newTestHandler(t, stub)

	res1, err := h.Pseudonymize(context.Background(), "Novartis A", "s1")
	require.NoError(t, err)
	res2, err := h.Pseudonymize(context.Background(), "Novartis A", "s1")
	require.NoError(t, err)
	require.Equal(t, res1.Text, res2.Text)
	require.Equal(t, 0, res2.NewMappingsCreated, "second call reuses mapping")
}

// With several languages each text is analyzed in its own: a German turn is
// sent to Presidio as "de", where the English model would miss German names.
func TestPseudonymize_AnalyzesInDetectedLanguage(t *testing.T) {
	mr := miniredis.RunT(t)
	store := mapping.NewRedisStoreFromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	stub := &stubAnalyzer{}
	h := NewHandler(HandlerConfig{
		Analyzer:  stub,
		Store:     store,
		Pools:     NewPools(map[string][]string{"PERSON": {"Alpha"}}),
		Entities:  []string{"PERSON"},
		Languages: []string{"en", "de"},
	})
	for _, text := range []string{"Bitte frag Anna Schmidt nach dem Vertrag", "Please ask John Smith about the contract"} {
		_, err := h.Pseudonymize(context.Background(), text, "s1")
		require.NoError(t, err)
	}
	require.Equal(t, []string{"de", "en"}, stub.languages)
}

// Each entity type is held to its own threshold: a 0.4 phone match passes its
// 0.4 override while a 0.5 PERSON match fails the 0.7 global threshold.
func TestPseudonymize_PerEntityThreshold(t *testing.T) {
	mr := miniredis.RunT(t)
	store := mapping.NewRedisStoreFromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	text := "Call Bob at 079 123 45 67"
	stub := &stubAnalyzer{detections: []presidio.Detection{
		{EntityType: "PERSON", Start: 5, End: 8, Score: 0.5, Text: "Bob"},
		{EntityType: "PHONE_NUMBER", Start: 12, End: 25, Score: 0.4, Text: "079 123 45 67"},
	}}
	h := NewHandler(HandlerConfig{
		Analyzer:         stub,
		Store:            store,
		Strategizer:      NewStrategizer(StrategizerConfig{Pools: NewPools(map[string][]string{"PERSON": {"Alpha"}}), Default: string(StrategyToken)}),
		Entities:         []string{"PERSON", "PHONE_NUMBER"},
		ScoreThreshold:   0.7,
		EntityThresholds: map[string]float64{"PHONE_NUMBER": 0.4},
	})
	res, err := h.Pseudonymize(context.Background(), text, "s1")
	require.NoError(t, err)
	require.Equal(t, 1, res.EntitiesDetected)
	require.Contains(t, res.Text, "Bob")
	require.NotContains(t, res.Text, "079 123 45 67")
}

func TestPseudonymize_StoredMappingAppliedWithoutNewDetection(t *testing.T) {
	// Turn 1 maps the name; in turn 2 the detector misses it. The stored
	// mapping must still hide it — before, the text went out unchanged.
	stub := &stubAnalyzer{detections: []presidio.Detection{{EntityType: "PERSON", Text: "Alice Johnson", Score: 0.9}}}
	h, _ := newDecomposingHandler(t, stub)
	_, err := h.Pseudonymize(context.Background(), "Book Alice Johnson.", "s1")
	require.NoError(t, err)

	stub.detections = nil
	res, err := h.Pseudonymize(context.Background(), "did alice johnson confirm?", "s1")
	require.NoError(t, err)
	require.Equal(t, "did jordan avery confirm?", res.Text)
	require.Equal(t, 0, res.EntitiesDetected)
}

func TestPseudonymize_ImplausiblePersonIgnored(t *testing.T) {
	// Production incident: the German spaCy model tagged this as PERSON and
	// it was rewritten to "taylor morgan", components included.
	stub := &stubAnalyzer{detections: []presidio.Detection{{EntityType: "PERSON", Text: "hi   which  passwd mgr", Score: 0.85}}}
	h, store := newDecomposingHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "hi   which  passwd mgr", "s1")
	require.NoError(t, err)
	require.Equal(t, "hi   which  passwd mgr", res.Text)
	m, err := store.GetMapping(context.Background(), "s1")
	require.NoError(t, err)
	require.Empty(t, m)
}

func TestPseudonymize_LowercaseNameKeepsNoWordMappings(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{{EntityType: "PERSON", Text: "thomas huber", Score: 0.95}}}
	h, store := newDecomposingHandler(t, stub)
	res, err := h.Pseudonymize(context.Background(), "frag mal den thomas huber", "s1")
	require.NoError(t, err)
	require.Equal(t, "frag mal den jordan avery", res.Text)
	m, err := store.GetMapping(context.Background(), "s1")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"thomas huber": "Jordan Avery"}, m, "no bare-word keys from a lower-case span")
}

func TestPseudonymize_AllowListedInflectionNotPseudonymized(t *testing.T) {
	stub := &stubAnalyzer{detections: []presidio.Detection{{EntityType: "PERSON", Text: "Linuxra", Score: 0.95}}}
	mr := miniredis.RunT(t)
	store := mapping.NewRedisStoreFromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		mapping.Config{SessionTTL: time.Minute, Timeout: time.Second}, nil)
	h := NewHandler(HandlerConfig{
		Analyzer:  stub,
		Store:     store,
		Pools:     NewPools(map[string][]string{"PERSON": {"Jordan Avery"}}),
		Entities:  []string{"PERSON"},
		AllowList: map[string]struct{}{"linux": {}},
	})
	res, err := h.Pseudonymize(context.Background(), "melyik jelszókezelő jó Linuxra?", "s1")
	require.NoError(t, err)
	require.Equal(t, "melyik jelszókezelő jó Linuxra?", res.Text)
}
