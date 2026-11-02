// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/text"
)

func TestNERCacheMetricsRegisterAndGather(t *testing.T) {
	reg := prometheus.NewRegistry()
	registerNERCacheMetrics(reg, text.NewNERCache(10, time.Hour))
	families, err := reg.Gather()
	require.NoError(t, err)
	got := map[string]int{}
	for _, f := range families {
		got[f.GetName()] = len(f.GetMetric())
	}
	require.Equal(t, map[string]int{
		"palena_pseudonymizer_ner_cache_lookups_total":   2, // hit, miss
		"palena_pseudonymizer_ner_cache_evictions_total": 1,
		"palena_pseudonymizer_ner_cache_entries":         1,
	}, got)
}
