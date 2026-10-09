// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package language

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	got, err := Parse([]string{" DE ", "en", "de", ""})
	require.NoError(t, err)
	require.Equal(t, []string{"de", "en"}, got)

	_, err = Parse([]string{"en", "xx"})
	require.ErrorContains(t, err, `"xx"`)
}

func TestFixed(t *testing.T) {
	require.Equal(t, "en", New(nil).Detect("Hallo Welt"))
	require.Equal(t, "de", New([]string{"de"}).Detect("Hello world"))
}

// Each message is analyzed in its own language, including short chat turns.
func TestLinguaDetects(t *testing.T) {
	d := New([]string{"en", "de", "fr", "it"})
	for text, want := range map[string]string{
		"Please ask John Smith about the contract":   "en",
		"Bitte frag Anna Schmidt nach dem Vertrag":   "de",
		"Peux-tu écrire à Marie Dupont demain matin": "fr",
		"Scrivi a Giulia Bianchi entro domani":       "it",
		"Grüezi Hans, wie gaht's?":                   "de",
	} {
		require.Equal(t, want, d.Detect(text), text)
	}
}

// A text with no language signal falls back to the first configured language.
func TestLinguaFallback(t *testing.T) {
	require.Equal(t, "de", New([]string{"de", "en"}).Detect("12345 !!!"))
}
