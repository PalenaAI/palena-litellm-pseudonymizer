// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"sort"
	"strings"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/audit"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/mapping"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

// Analyzer is the subset of *presidio.Analyzer the text handler needs.
// A tiny interface lets tests inject a stub instead of running a full
// httptest server.
type Analyzer interface {
	Analyze(ctx context.Context, text string, entities []string, language string) ([]presidio.Detection, error)
}

// Handler orchestrates: analyze → assign pseudonyms → persist → rewrite.
// It is the equivalent of the Python TextHandler.
type Handler struct {
	analyzer  Analyzer
	store     mapping.Store
	strat     *Strategizer
	replacer  *Replacer
	entities  []string
	language  string
	decompose bool
}

// HandlerConfig groups constructor parameters.
type HandlerConfig struct {
	Analyzer Analyzer
	Store    mapping.Store
	// Pools is used to build a default pool-only Strategizer when
	// Strategizer is nil (keeps older callers/tests working).
	Pools *Pools
	// Strategizer selects the per-entity substitution strategy. When nil,
	// a pool-only strategizer is built from Pools.
	Strategizer *Strategizer
	Entities    []string
	Language    string
	// DecomposePersonNames, when true, registers first-name and
	// last-name sub-mappings for multi-token PERSON entities so that
	// bare first/last-name references stay consistent with the
	// full-name pseudonym. See ComponentMappings.
	DecomposePersonNames bool
}

// NewHandler builds a text handler. All fields are required except
// Language, which defaults to "en".
func NewHandler(cfg HandlerConfig) *Handler {
	lang := cfg.Language
	if lang == "" {
		lang = "en"
	}
	strat := cfg.Strategizer
	if strat == nil {
		// Backward-compatible default: everything uses the pool strategy.
		strat = NewStrategizer(StrategizerConfig{Pools: cfg.Pools, Default: string(StrategyPool)})
	}
	return &Handler{
		analyzer:  cfg.Analyzer,
		store:     cfg.Store,
		strat:     strat,
		replacer:  NewReplacer(),
		entities:  cfg.Entities,
		language:  lang,
		decompose: cfg.DecomposePersonNames,
	}
}

// Replacer returns the underlying text replacer (used by streaming).
func (h *Handler) Replacer() *Replacer { return h.replacer }

// PseudonymizeResult carries the rewritten text plus counters.
type PseudonymizeResult struct {
	Text               string
	EntitiesDetected   int
	NewMappingsCreated int
	EntityTypeCounts   map[audit.EntityType]int
	SessionMappingSize int
}

// Pseudonymize runs the pre-call flow for a single text string.
//
//  1. Analyze via Presidio (fail-closed).
//  2. Filter to configured entity types.
//  3. Load existing session mapping.
//  4. Assign new pseudonyms (existing entries win).
//  5. Persist the merged mapping.
//  6. Rewrite the text with the merged mapping.
func (h *Handler) Pseudonymize(ctx context.Context, text, sessionID string) (*PseudonymizeResult, error) {
	res := &PseudonymizeResult{
		Text:             text,
		EntityTypeCounts: map[audit.EntityType]int{},
	}
	if strings.TrimSpace(text) == "" {
		return res, nil
	}

	detections, err := h.analyzer.Analyze(ctx, text, h.entities, h.language)
	if err != nil {
		return nil, err
	}
	filtered := filterByEntityType(detections, h.entities)
	res.EntitiesDetected = len(filtered)

	for _, d := range filtered {
		res.EntityTypeCounts[audit.NormalizeEntityType(d.EntityType)]++
	}

	if len(filtered) == 0 {
		merged, err := h.store.GetMapping(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		res.SessionMappingSize = len(merged)
		return res, nil
	}

	existing, err := h.store.GetMapping(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	newMappings := h.assignNew(filtered, existing)
	res.NewMappingsCreated = len(newMappings)

	var merged map[string]string
	if len(newMappings) == 0 {
		merged = existing
	} else {
		merged, err = h.store.AddMappings(ctx, sessionID, newMappings)
		if err != nil {
			return nil, err
		}
	}
	res.SessionMappingSize = len(merged)
	res.Text = h.replacer.Replace(text, merged)
	return res, nil
}

// Reverse runs the post-call flow: load the reverse map and rewrite.
// Empty reverse map is a no-op (returns text unchanged).
func (h *Handler) Reverse(ctx context.Context, text, sessionID string) (string, error) {
	if text == "" {
		return text, nil
	}
	reverse, err := h.store.GetReverseMapping(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if len(reverse) == 0 {
		return text, nil
	}
	return h.replacer.Replace(text, reverse), nil
}

// ReverseStreamResult carries the parallel outputs of ReverseStream.
type ReverseStreamResult struct {
	Texts     []string
	Holdbacks []int
	Modified  bool
}

// ReverseStream is the incremental_diff streaming variant of Reverse.
// Loads the reverse map once and, for each input text, returns the
// rewritten text plus a per-index holdback char count so the framework
// buffers partial pseudonyms until they fully arrive.
//
// See context/PSEUDONYMIZATION_ALGORITHM.md § "Streaming holdback".
func (h *Handler) ReverseStream(ctx context.Context, texts []string, sessionID string) (*ReverseStreamResult, error) {
	out := &ReverseStreamResult{
		Texts:     make([]string, len(texts)),
		Holdbacks: make([]int, len(texts)),
	}
	if len(texts) == 0 {
		return out, nil
	}
	reverse, err := h.store.GetReverseMapping(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(reverse) == 0 {
		copy(out.Texts, texts)
		return out, nil
	}
	keys := make([]string, 0, len(reverse))
	for k := range reverse {
		keys = append(keys, k)
	}
	for i, raw := range texts {
		rewritten := h.replacer.Replace(raw, reverse)
		out.Texts[i] = rewritten
		out.Holdbacks[i] = ComputeHoldback(rewritten, keys)
		if rewritten != raw {
			out.Modified = true
		}
	}
	return out, nil
}

// assignNew selects pseudonyms for detections not already in the
// session mapping. Sorted longest-first, then alphabetical, so compound
// names get assigned before their components inside one request.
func (h *Handler) assignNew(detections []presidio.Detection, existing map[string]string) map[string]string {
	// De-dup by text (case-preserving) — one entity name → one mapping.
	// When the same span is classified as multiple types (e.g. a card
	// number tagged both CREDIT_CARD and, by a noisy NER, ORGANIZATION),
	// keep the higher-scoring classification so the right strategy applies.
	byText := make(map[string]presidio.Detection, len(detections))
	for _, d := range detections {
		trimmed := strings.TrimSpace(d.Text)
		if trimmed == "" {
			continue
		}
		if prev, ok := byText[trimmed]; ok && prev.Score >= d.Score {
			continue
		}
		byText[trimmed] = presidio.Detection{
			EntityType: d.EntityType,
			Text:       trimmed,
			Score:      d.Score,
		}
	}
	sorted := make([]presidio.Detection, 0, len(byText))
	for _, d := range byText {
		sorted = append(sorted, d)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i].Text) != len(sorted[j].Text) {
			return len(sorted[i].Text) > len(sorted[j].Text)
		}
		return sorted[i].Text < sorted[j].Text
	})

	// Scratch mirrors what the merged mapping will be after we apply
	// this request's assignments; ensures no two new entities collide
	// on the same pseudonym.
	scratch := make(map[string]string, len(existing))
	for k, v := range existing {
		scratch[k] = v
	}

	newMap := map[string]string{}
	for _, d := range sorted {
		if _, ok := scratch[d.Text]; ok {
			continue
		}
		if hasCaseInsensitiveKey(scratch, d.Text) {
			continue
		}
		p := h.strat.Assign(d.EntityType, d.Text, scratch)
		newMap[d.Text] = p
		scratch[d.Text] = p

		// Register first/last-name sub-mappings so bare references
		// (model shortening "Jordan Avery" → "Jordan", or a later user
		// message saying just "Alice") stay consistent. Existing-wins:
		// a component already mapped for another person is left alone.
		if h.decompose && d.EntityType == "PERSON" {
			for cReal, cPseudo := range ComponentMappings(d.Text, p) {
				if _, ok := scratch[cReal]; ok {
					continue
				}
				if hasCaseInsensitiveKey(scratch, cReal) {
					continue
				}
				newMap[cReal] = cPseudo
				scratch[cReal] = cPseudo
			}
		}
	}
	return newMap
}

func filterByEntityType(detections []presidio.Detection, allowed []string) []presidio.Detection {
	if len(allowed) == 0 {
		return detections
	}
	set := make(map[string]struct{}, len(allowed))
	for _, e := range allowed {
		set[e] = struct{}{}
	}
	out := make([]presidio.Detection, 0, len(detections))
	for _, d := range detections {
		if _, ok := set[d.EntityType]; ok {
			out = append(out, d)
		}
	}
	return out
}

func hasCaseInsensitiveKey(m map[string]string, key string) bool {
	kl := strings.ToLower(key)
	for k := range m {
		if strings.ToLower(k) == kl {
			return true
		}
	}
	return false
}
