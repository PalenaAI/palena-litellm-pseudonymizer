// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// unit is one piece of a text a span model sees on its own: Text starts at
// byte offset Offset of the original.
type unit struct {
	Offset int
	Text   string
}

// splitUnits cuts text into the pieces a span model analyzes: one per line —
// the Wäg pre_call scan joins messages with "\n", and a model that sees many
// unrelated messages at once loses confidence on the names in them — with any
// line longer than maxChars characters cut into overlapping windows (see
// windowLine). Blank lines are dropped. Offsets are bytes into text.
func splitUnits(text string, maxChars, overlap int) []unit {
	var out []unit
	pos := 0
	for {
		nl := strings.IndexByte(text[pos:], '\n')
		end := len(text)
		if nl >= 0 {
			end = pos + nl
		}
		line := text[pos:end]
		if strings.TrimSpace(line) != "" {
			for _, w := range windowLine(line, maxChars, overlap) {
				out = append(out, unit{Offset: pos + w.Offset, Text: w.Text})
			}
		}
		if nl < 0 {
			return out
		}
		pos = end + 1
	}
}

// windowLine cuts a line into windows of at most maxChars characters. Each cut
// prefers a sentence end, then whitespace, inside the last `overlap`
// characters; the next window starts `overlap` characters before the cut (on a
// word start) so a name on a boundary is seen whole at least once. A line that
// fits, or maxChars <= 0, is one window.
func windowLine(line string, maxChars, overlap int) []unit {
	if maxChars <= 0 || utf8.RuneCountInString(line) <= maxChars {
		return []unit{{Offset: 0, Text: line}}
	}
	if overlap < 0 || overlap >= maxChars/2 {
		overlap = maxChars / 4
	}
	// Byte offset of every character, plus the end.
	idx := make([]int, 0, len(line)+1)
	for i := range line {
		idx = append(idx, i)
	}
	idx = append(idx, len(line))
	n := len(idx) - 1 // characters

	var out []unit
	start := 0
	for start < n {
		end := min(start+maxChars, n)
		if end < n {
			end = cutAt(line, idx, start, end, overlap)
		}
		out = append(out, unit{Offset: idx[start], Text: line[idx[start]:idx[end]]})
		if end >= n {
			break
		}
		next := max(end-overlap, start+1)
		for next < end && !isSpaceAt(line, idx, next-1) {
			next++
		}
		start = next
	}
	return out
}

// cutAt picks the window end (a character index) inside [end-overlap, end].
func cutAt(line string, idx []int, start, end, overlap int) int {
	lo := max(start+1, end-overlap)
	for i := end - 1; i > lo; i-- {
		// After a sentence end followed by whitespace.
		if isSpaceAt(line, idx, i) && strings.ContainsRune(".!?;:", runeAt(line, idx, i-1)) {
			return i + 1
		}
	}
	for i := end - 1; i >= lo; i-- {
		if isSpaceAt(line, idx, i) {
			return i + 1
		}
	}
	return end
}

func runeAt(s string, idx []int, i int) rune {
	r, _ := utf8.DecodeRuneInString(s[idx[i]:])
	return r
}

func isSpaceAt(s string, idx []int, i int) bool { return unicode.IsSpace(runeAt(s, idx, i)) }

// charToByte maps character offsets inside s to byte offsets; ok is false
// when an offset lies outside s.
func charToByte(s string, chars ...int) (out []int, ok bool) {
	idx := make([]int, 0, len(s)+1)
	for i := range s {
		idx = append(idx, i)
	}
	idx = append(idx, len(s))
	out = make([]int, len(chars))
	for k, c := range chars {
		if c < 0 || c >= len(idx) {
			return nil, false
		}
		out[k] = idx[c]
	}
	return out, true
}
