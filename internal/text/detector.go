// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/language"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/ner"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

// Detector finds the entities in one text. Offsets in the returned
// detections are byte offsets into text, Text is the span itself.
type Detector interface {
	Detect(ctx context.Context, text string) ([]presidio.Detection, error)
}

// PresidioDetector is the "presidio" engine: every entity type from Presidio,
// the text analyzed in the language the detector picks for it.
type PresidioDetector struct {
	Analyzer Analyzer
	Entities []string
	Language language.Detector
}

// Detect implements Detector.
func (d *PresidioDetector) Detect(ctx context.Context, text string) ([]presidio.Detection, error) {
	return d.Analyzer.Analyze(ctx, text, d.Entities, d.Language.Detect(text))
}

// NERClient is the subset of *ner.Client the NER engine needs.
type NERClient interface {
	Detect(ctx context.Context, texts, labels []string, threshold float64) ([][]ner.Span, error)
}

// NERDetector is the "ner" engine: name-like entity types (PERSON, and
// whatever else Labels maps) from the span model — one call per message,
// no language needed — and the remaining configured types (email, phone,
// IBAN, card, IP, …) from Presidio's pattern recognizers, which work the
// same in every language.
type NERDetector struct {
	NER NERClient
	// Labels maps each model label to the entity type it reports
	// ("full_name" → PERSON). Only labels whose type is enabled are asked.
	Labels    map[string]string
	Threshold float64
	// ChunkChars / ChunkOverlap: lines longer than ChunkChars characters
	// are cut into overlapping windows (the model is trained on short
	// inputs; past ~512 tokens it stops finding names).
	ChunkChars   int
	ChunkOverlap int

	// Cache, when set, answers lines the model has already seen (see
	// NERCache); only the others are sent.
	Cache *NERCache
	// BatchSize lines go to the service per call (default 16); up to
	// Parallel calls run at once (default 1). Each call's result is cached as
	// soon as it returns, so a request the caller abandons still leaves its
	// finished batches behind for the next attempt.
	BatchSize int
	Parallel  int

	// Deny finds the always-pseudonymized terms of the types the span model
	// serves: those types never reach Presidio, so its deny-list recognizers
	// would never be asked. Nil when there are none.
	Deny *DenyMatcher

	// Presidio handles PatternEntities, always in PatternLanguage.
	Analyzer        Analyzer
	PatternEntities []string
	PatternLanguage string
}

// spansFor returns the model's spans for every text, parallel to texts:
// cached lines from the cache, the rest (each distinct text once) in batches
// of BatchSize, up to Parallel at a time. A batch is cached as soon as it
// returns — never only at the end — so when the caller gives up (a gateway
// timeout on a long conversation) the next attempt only sends what is left.
func (d *NERDetector) spansFor(ctx context.Context, texts, labels []string) ([][]ner.Span, error) {
	out := make([][]ner.Span, len(texts))
	scope := ""
	if d.Cache != nil {
		scope = strings.Join(labels, "|") + "|" + strconv.FormatFloat(d.Threshold, 'g', -1, 64)
	}
	var ask []string
	askOf := map[string]int{} // text → index in ask
	pending := make([]int, len(texts))
	for i, t := range texts {
		pending[i] = -1
		if d.Cache != nil {
			if spans, ok := d.Cache.get(nerCacheKey(scope, t)); ok {
				out[i] = fromCached(spans)
				continue
			}
		}
		j, seen := askOf[t]
		if !seen {
			j = len(ask)
			askOf[t] = j
			ask = append(ask, t)
		}
		pending[i] = j
	}
	if len(ask) == 0 {
		return out, nil
	}
	results, err := d.detectBatches(ctx, ask, labels, scope)
	if err != nil {
		return nil, err
	}
	for i, j := range pending {
		if j >= 0 {
			out[i] = results[j]
		}
	}
	return out, nil
}

// detectBatches asks the service for every text in ask, BatchSize per call
// and up to Parallel calls at once, caching each batch on return. The first
// failure stops the batches not yet started; finished ones stay cached.
func (d *NERDetector) detectBatches(ctx context.Context, ask, labels []string, scope string) ([][]ner.Span, error) {
	size := d.BatchSize
	if size <= 0 {
		size = 16
	}
	parallel := max(d.Parallel, 1)
	results := make([][]ner.Span, len(ask))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		finished int // batches whose results are in
		wg       sync.WaitGroup
	)
	batches := (len(ask) + size - 1) / size
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel() // siblings stop; their own errors are not the cause
		}
		mu.Unlock()
	}
	slots := make(chan struct{}, parallel)
	for start := 0; start < len(ask); start += size {
		end := min(start+size, len(ask))
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break // a batch failed, or the caller gave up: start nothing new
		}
		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			defer func() { <-slots }()
			batch := ask[start:end]
			res, err := d.NER.Detect(ctx, batch, labels, d.Threshold)
			if err == nil && len(res) != len(batch) {
				err = errs.Wrap(errs.ErrNERMalformed, "%d results for %d texts", len(res), len(batch))
			}
			if err != nil {
				fail(err)
				return
			}
			copy(results[start:end], res)
			if d.Cache != nil {
				for k, t := range batch {
					d.Cache.put(nerCacheKey(scope, t), toCached(res[k], labels))
				}
			}
			mu.Lock()
			finished++
			mu.Unlock()
		}(start, end)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if finished < batches {
		// The caller's context ended between batches (no call failed).
		return nil, errs.SendFailure(ctx, ctx.Err(), errs.ErrNERUnavailable, "ner detect", 0)
	}
	return results, nil
}

func fromCached(spans []cachedSpan) []ner.Span {
	if len(spans) == 0 {
		return nil
	}
	out := make([]ner.Span, len(spans))
	for i, s := range spans {
		out[i] = ner.Span{Label: s.label, Start: int(s.start), End: int(s.end), Score: s.score}
	}
	return out
}

// toCached keeps the spans of labels that were asked for, pointing at the
// detector's label strings so a cached entry holds no copy of them.
func toCached(spans []ner.Span, labels []string) []cachedSpan {
	var out []cachedSpan
	for _, s := range spans {
		for _, l := range labels {
			if s.Label == l {
				out = append(out, cachedSpan{start: int32(s.Start), end: int32(s.End), score: s.Score, label: l})
				break
			}
		}
	}
	return out
}

// NERLabelsFor returns the model labels whose entity type is in entities,
// and the entity types that are left for Presidio.
func NERLabelsFor(labels map[string]string, entities []string) (asked map[string]string, rest []string) {
	enabled := map[string]bool{}
	for _, e := range entities {
		enabled[e] = true
	}
	asked = map[string]string{}
	viaNER := map[string]bool{}
	for label, typ := range labels {
		if enabled[typ] {
			asked[label] = typ
			viaNER[typ] = true
		}
	}
	for _, e := range entities {
		if !viaNER[e] {
			rest = append(rest, e)
		}
	}
	return asked, rest
}

// Detect implements Detector.
func (d *NERDetector) Detect(ctx context.Context, text string) ([]presidio.Detection, error) {
	var out []presidio.Detection
	if len(d.Labels) > 0 {
		spans, err := d.detectNames(ctx, text)
		if err != nil {
			return nil, err
		}
		out = spans
	}
	out = append(out, d.Deny.Find(text)...)
	if len(d.PatternEntities) > 0 {
		pat, err := d.Analyzer.Analyze(ctx, text, d.PatternEntities, d.PatternLanguage)
		if err != nil {
			return nil, err
		}
		out = append(out, pat...)
	}
	return out, nil
}

func (d *NERDetector) detectNames(ctx context.Context, text string) ([]presidio.Detection, error) {
	units := splitUnits(text, d.ChunkChars, d.ChunkOverlap)
	if len(units) == 0 {
		return nil, nil
	}
	texts := make([]string, len(units))
	for i, u := range units {
		texts[i] = u.Text
	}
	labels := make([]string, 0, len(d.Labels))
	for l := range d.Labels {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	results, err := d.spansFor(ctx, texts, labels)
	if err != nil {
		return nil, err
	}

	// Overlapping windows report the same name twice: keep one per
	// (span, type), the highest score.
	type key struct {
		start, end int
		typ        string
	}
	best := map[key]presidio.Detection{}
	for i, spans := range results {
		u := units[i]
		for _, s := range spans {
			typ, ok := d.Labels[s.Label]
			if !ok || s.Start >= s.End {
				continue
			}
			b, ok := charToByte(u.Text, s.Start, s.End)
			if !ok {
				return nil, errs.Wrap(errs.ErrNERMalformed, "span %d-%d outside a %d-byte text", s.Start, s.End, len(u.Text))
			}
			start, end := u.Offset+b[0], u.Offset+b[1]
			k := key{start, end, typ}
			if prev, seen := best[k]; seen && prev.Score >= s.Score {
				continue
			}
			best[k] = presidio.Detection{EntityType: typ, Start: start, End: end, Score: s.Score, Text: text[start:end]}
		}
	}
	out := make([]presidio.Detection, 0, len(best))
	for _, det := range best {
		out = append(out, det)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out, nil
}
