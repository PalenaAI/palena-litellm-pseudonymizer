// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"sort"

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

	// Presidio handles PatternEntities, always in PatternLanguage.
	Analyzer        Analyzer
	PatternEntities []string
	PatternLanguage string
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

	results, err := d.NER.Detect(ctx, texts, labels, d.Threshold)
	if err != nil {
		return nil, err
	}
	if len(results) != len(units) {
		return nil, errs.Wrap(errs.ErrNERMalformed, "%d results for %d texts", len(results), len(units))
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
