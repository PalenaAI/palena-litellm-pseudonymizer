// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/ner"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

// stubNER answers each text with the character spans of the names it knows.
type stubNER struct {
	names  map[string]string // surface → label
	texts  []string
	labels []string
	thr    float64
	err    error
}

func (s *stubNER) Detect(_ context.Context, texts, labels []string, threshold float64) ([][]ner.Span, error) {
	s.texts, s.labels, s.thr = texts, labels, threshold
	if s.err != nil {
		return nil, s.err
	}
	out := make([][]ner.Span, len(texts))
	for i, t := range texts {
		for name, label := range s.names {
			if b := strings.Index(t, name); b >= 0 {
				start := len([]rune(t[:b]))
				out[i] = append(out[i], ner.Span{Label: label, Start: start, End: start + len([]rune(name)), Score: 0.95})
			}
		}
	}
	return out, nil
}

func TestNERLabelsFor_RoutesEnabledTypes(t *testing.T) {
	labels := map[string]string{"person": "PERSON", "full_name": "PERSON", "organization": "ORGANIZATION"}
	asked, rest := NERLabelsFor(labels, []string{"PERSON", "EMAIL_ADDRESS", "IBAN_CODE"})
	require.Equal(t, map[string]string{"person": "PERSON", "full_name": "PERSON"}, asked)
	require.Equal(t, []string{"EMAIL_ADDRESS", "IBAN_CODE"}, rest, "types the model does not serve go to Presidio")
}

func TestNERDetector_PerLineSpansMapToByteOffsets(t *testing.T) {
	text := "Please loop in Sabine Keller.\nKérlek, küldd el Kovács Péternek is.\nMerci"
	stub := &stubNER{names: map[string]string{"Sabine Keller": "person", "Kovács Péter": "full_name"}}
	pat := &stubAnalyzer{detections: []presidio.Detection{{EntityType: "EMAIL_ADDRESS", Start: 0, End: 1, Score: 1}}}
	d := &NERDetector{
		NER: stub, Labels: map[string]string{"person": "PERSON", "full_name": "PERSON"}, Threshold: 0.9,
		ChunkChars: 1000, ChunkOverlap: 150,
		Analyzer: pat, PatternEntities: []string{"EMAIL_ADDRESS"}, PatternLanguage: "en",
	}
	got, err := d.Detect(context.Background(), text)
	require.NoError(t, err)

	require.Len(t, stub.texts, 3, "one unit per message line")
	require.Equal(t, []string{"full_name", "person"}, stub.labels)
	require.InDelta(t, 0.9, stub.thr, 1e-9)
	require.Equal(t, []string{"en"}, pat.languages, "pattern types always in the configured language")

	var names []string
	for _, det := range got {
		if det.EntityType == "PERSON" {
			require.Equal(t, det.Text, text[det.Start:det.End], "byte offsets into the full text")
			names = append(names, det.Text)
		}
	}
	require.Equal(t, []string{"Sabine Keller", "Kovács Péter"}, names)
}

func TestNERDetector_OverlappingWindowsReportANameOnce(t *testing.T) {
	line := strings.Repeat("filler words here. ", 20) + "Sabine Keller signed. " + strings.Repeat("more filler text. ", 20)
	stub := &stubNER{names: map[string]string{"Sabine Keller": "person"}}
	d := &NERDetector{NER: stub, Labels: map[string]string{"person": "PERSON"}, Threshold: 0.9, ChunkChars: 300, ChunkOverlap: 120}
	got, err := d.Detect(context.Background(), line)
	require.NoError(t, err)
	require.Greater(t, len(stub.texts), 1, "the long line was windowed")
	require.Len(t, got, 1)
	require.Equal(t, "Sabine Keller", got[0].Text)
}

func TestNERDetector_FailsClosed(t *testing.T) {
	d := &NERDetector{NER: &stubNER{err: errs.ErrNERUnavailable}, Labels: map[string]string{"person": "PERSON"}, ChunkChars: 1000}
	_, err := d.Detect(context.Background(), "Sabine Keller")
	require.ErrorIs(t, err, errs.ErrNERUnavailable)
}

func TestNERDetector_SpanOutsideTextIsMalformed(t *testing.T) {
	bad := &badSpanNER{}
	d := &NERDetector{NER: bad, Labels: map[string]string{"person": "PERSON"}, ChunkChars: 1000}
	_, err := d.Detect(context.Background(), "abc")
	require.ErrorIs(t, err, errs.ErrNERMalformed)
}

type badSpanNER struct{}

func (badSpanNER) Detect(_ context.Context, texts, _ []string, _ float64) ([][]ner.Span, error) {
	return [][]ner.Span{{{Label: "person", Start: 0, End: 99, Score: 1}}}, nil
}

func TestNERDetector_DenyListsForModelTypes(t *testing.T) {
	stub := &stubNER{names: map[string]string{}}
	d := &NERDetector{
		NER: stub, Labels: map[string]string{"person": "PERSON"}, Threshold: 0.9,
		ChunkChars: 1000, ChunkOverlap: 150,
		Deny: NewDenyMatcher([]DenyList{{Entity: "PERSON", Terms: []string{"Project Falcon"}}}, []string{"PERSON"}),
	}
	got, err := d.Detect(context.Background(), "Status of project falcon?")
	require.NoError(t, err)
	require.Len(t, got, 1, "a term the model misses is still found")
	require.Equal(t, "PERSON", got[0].EntityType)
	require.Equal(t, "project falcon", got[0].Text)
}
