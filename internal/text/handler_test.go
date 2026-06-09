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

	"github.com/bitkaio/palena-litellm-pseudonymizer-service/internal/mapping"
	"github.com/bitkaio/palena-litellm-pseudonymizer-service/internal/presidio"
)

type stubAnalyzer struct {
	detections []presidio.Detection
	err        error
	calls      int
}

func (s *stubAnalyzer) Analyze(_ context.Context, _ string, _ []string, _ string) ([]presidio.Detection, error) {
	s.calls++
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
