// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/presidio"
)

// DenyList is one entity type's always-pseudonymized terms
// (PALENA_PSEUDONYMIZER_DENY_LISTS, the chart's presidio.analyzer.denyLists).
type DenyList struct {
	Entity string   `json:"entity"`
	Terms  []string `json:"terms"`
}

// denyMatch is the score of a deny-list hit: an exact match is certain, so it
// clears every threshold.
const denyMatch = 1.0

// DenyMatcher finds deny-listed terms in a text the way Presidio's deny-list
// recognizer does: case-insensitively, as whole words — the characters next to
// a match must not be letters, digits or "_".
//
// The "presidio" engine leaves deny-lists to Presidio's recognizers. The "ner"
// engine needs this: the types the span model serves (PERSON, …) are never
// sent to Presidio, so a recognizer there would never be asked.
type DenyMatcher struct {
	byEntity map[string]*regexp.Regexp
}

// NewDenyMatcher compiles the lists of the given entity types; lists of other
// types, and empty terms, are skipped. Nil when nothing is left.
func NewDenyMatcher(lists []DenyList, entities []string) *DenyMatcher {
	enabled := map[string]bool{}
	for _, e := range entities {
		enabled[e] = true
	}
	terms := map[string][]string{}
	for _, l := range lists {
		if !enabled[l.Entity] {
			continue
		}
		for _, t := range l.Terms {
			if t = strings.TrimSpace(t); t != "" {
				terms[l.Entity] = append(terms[l.Entity], t)
			}
		}
	}
	if len(terms) == 0 {
		return nil
	}
	m := &DenyMatcher{byEntity: map[string]*regexp.Regexp{}}
	for entity, ts := range terms {
		// Longest first, so "Contoso Ltd" wins over "Contoso".
		sort.Slice(ts, func(i, j int) bool { return len(ts[i]) > len(ts[j]) })
		quoted := make([]string, len(ts))
		for i, t := range ts {
			quoted[i] = regexp.QuoteMeta(t)
		}
		m.byEntity[entity] = regexp.MustCompile("(?i)(?:" + strings.Join(quoted, "|") + ")")
	}
	return m
}

// Find returns every whole-word occurrence of a deny-listed term in text, as
// byte-offset detections of the term's entity type.
func (m *DenyMatcher) Find(text string) []presidio.Detection {
	if m == nil {
		return nil
	}
	var out []presidio.Detection
	for entity, re := range m.byEntity {
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if !wordBoundaryBefore(text, loc[0]) || !wordBoundaryAfter(text, loc[1]) {
				continue
			}
			out = append(out, presidio.Detection{
				EntityType: entity, Start: loc[0], End: loc[1], Score: denyMatch, Text: text[loc[0]:loc[1]],
			})
		}
	}
	return out
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func wordBoundaryBefore(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return !isWordRune(r)
}

func wordBoundaryAfter(text string, i int) bool {
	if i == len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[i:])
	return !isWordRune(r)
}
