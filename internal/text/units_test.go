// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestSplitUnits_OneUnitPerNonBlankLine(t *testing.T) {
	text := "hi Sabine\n\n  \nKérlek, küldd el Kovács Péternek.\nlast"
	units := splitUnits(text, 1000, 150)
	require.Len(t, units, 3)
	for _, u := range units {
		require.Equal(t, u.Text, text[u.Offset:u.Offset+len(u.Text)], "offsets are bytes into the original")
		require.NotContains(t, u.Text, "\n")
	}
	require.Equal(t, "Kérlek, küldd el Kovács Péternek.", units[1].Text)
}

func TestSplitUnits_SingleLineIsOneUnit(t *testing.T) {
	require.Equal(t, []unit{{Offset: 0, Text: "Tell James I'll be late."}}, splitUnits("Tell James I'll be late.", 1000, 150))
}

func TestWindowLine_CoversLongLineWithOverlap(t *testing.T) {
	// 50 sentences of non-ASCII text on one line: windows must cover every
	// byte, never exceed the character limit, and see each name whole once.
	line := strings.Repeat("Μαρία Παπαδοπούλου υπέγραψε τη σύμβαση σήμερα. ", 50)
	ws := windowLine(line, 300, 60)
	require.Greater(t, len(ws), 5)
	covered := make([]bool, len(line))
	for _, w := range ws {
		require.Equal(t, w.Text, line[w.Offset:w.Offset+len(w.Text)])
		require.LessOrEqual(t, utf8.RuneCountInString(w.Text), 300)
		require.True(t, utf8.ValidString(w.Text), "windows cut on character boundaries")
		for i := w.Offset; i < w.Offset+len(w.Text); i++ {
			covered[i] = true
		}
	}
	for i, c := range covered {
		require.True(t, c, "byte %d not covered", i)
	}
	whole := 0
	for _, w := range ws {
		whole += strings.Count(w.Text, "Μαρία Παπαδοπούλου")
	}
	require.GreaterOrEqual(t, whole, 50, "every occurrence appears whole in some window")
}

func TestCharToByte(t *testing.T) {
	got, ok := charToByte("Zürich Müller", 7, 13)
	require.True(t, ok)
	require.Equal(t, "Müller", "Zürich Müller"[got[0]:got[1]])
	_, ok = charToByte("abc", 4)
	require.False(t, ok)
}
