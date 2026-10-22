// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDenyMatcher_WholeWordsCaseInsensitive(t *testing.T) {
	m := NewDenyMatcher([]DenyList{
		{Entity: "PERSON", Terms: []string{"Project Falcon", "Zoë"}},
		{Entity: "ORGANIZATION", Terms: []string{"Contoso", "Contoso Ltd"}},
	}, []string{"PERSON", "ORGANIZATION"})

	text := "Ask zoë about project falcon at Contoso Ltd, not Contosoft or Zoëlle."
	got := map[string]string{}
	for _, d := range m.Find(text) {
		require.Equal(t, d.Text, text[d.Start:d.End], "byte offsets")
		require.InDelta(t, 1.0, d.Score, 0)
		got[d.Text] = d.EntityType
	}
	require.Equal(t, map[string]string{
		"zoë": "PERSON", "project falcon": "PERSON", "Contoso Ltd": "ORGANIZATION",
	}, got, "longest term wins; no match inside a longer word")
}

func TestDenyMatcher_ShorterTermWhenLongerFailsBoundary(t *testing.T) {
	m := NewDenyMatcher([]DenyList{
		{Entity: "ORGANIZATION", Terms: []string{"Contoso", "Contoso Ltd"}},
	}, []string{"ORGANIZATION"})

	// "Contoso Ltd" is not a whole word in "Contoso Ltda"; "Contoso" is, and
	// must still be found rather than leaking the deny-listed name.
	text := "Contoso Ltda signed; Contoso Ltd too."
	var got []string
	for _, d := range m.Find(text) {
		got = append(got, d.Text)
	}
	require.ElementsMatch(t, []string{"Contoso", "Contoso Ltd"}, got)
}

func TestDenyMatcher_OnlyEnabledTypes(t *testing.T) {
	lists := []DenyList{{Entity: "ORGANIZATION", Terms: []string{"Contoso"}}, {Entity: "PERSON", Terms: []string{" "}}}
	require.Nil(t, NewDenyMatcher(lists, []string{"PERSON"}), "ORGANIZATION not enabled, PERSON list empty")
	require.Nil(t, (*DenyMatcher)(nil).Find("Contoso"))
}
