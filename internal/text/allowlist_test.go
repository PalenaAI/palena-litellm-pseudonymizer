// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowListed(t *testing.T) {
	set := map[string]struct{}{"linux": {}, "docker": {}, "proton pass": {}, "oomkilled": {}, "go": {}}
	for _, span := range []string{"Linux", " linux ", "Linuxra", "Linuxon", "Dockerrel", "Docker's", "Proton Pass", "OOMKilled-del"} {
		require.True(t, AllowListed(set, span), span)
	}
	for _, span := range []string{
		"Linus",               // different word
		"Linuxdistribution",   // ending too long to be an inflection
		"Proton Pass Johnson", // term among other words may be a person
		"Docker 2",            // not a letter ending
		"Gopher",              // two-letter terms match exactly only
		"",
	} {
		require.False(t, AllowListed(set, span), span)
	}
}

func TestBuiltinAllowList_HasNoCommonGivenNames(t *testing.T) {
	// These are product names AND people's names: allow-listing them by
	// default would leak a person called Claude or Alexa.
	set := map[string]struct{}{}
	for _, term := range BuiltinAllowList {
		set[term] = struct{}{}
	}
	for _, name := range []string{"Alexa", "Siri", "Claude", "Jenkins", "Kafka", "Render"} {
		_, ok := set[name]
		require.False(t, ok, name)
	}
}

func TestPlausiblePerson(t *testing.T) {
	for _, s := range []string{"Sabine Keller", "thomas huber", "simone de luca", "Pieter van der Berg", "Μαρία Παπαδοπούλου", "Kovács Péter", "peter", "Ola", "zoli"} {
		require.True(t, plausiblePerson(s), s)
	}
	for _, s := range []string{
		"hi   which  passwd mgr", // the production incident
		"which password manager should",
		"mgr", "brb", // chat-speak the span model tags as a first name
		"A B C D E F",
		"1234",
		"",
	} {
		require.False(t, plausiblePerson(s), s)
	}
}
