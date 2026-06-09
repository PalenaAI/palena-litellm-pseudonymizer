// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_API_KEY", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "http://presidio-analyzer:5001", cfg.AnalyzerURL)
	require.Equal(t, "redis://redis:6379/0", cfg.RedisURL)
	require.Equal(t, "palena:pseudonymizer", cfg.RedisKeyPrefix)
	require.Contains(t, cfg.PoolPerson, "Jordan Avery")
	require.Equal(t, "redact", cfg.PIIAction)
	// LOCATION is intentionally excluded from the default entity set to
	// avoid city-name geography drift.
	require.Equal(t, []string{"PERSON", "ORGANIZATION"}, cfg.Entities)
	require.NotContains(t, cfg.Entities, "LOCATION")
}

func TestLoad_InvalidThreshold(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_PRESIDIO_SCORE_THRESHOLD", "1.5")
	_, err := Load()
	require.Error(t, err)
}

func TestLoad_BadPIIAction(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_NON_TEXT_PII_ACTION", "murder")
	_, err := Load()
	require.Error(t, err)
}

func TestLoad_BadBlockMessage(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_NON_TEXT_BLOCK_MESSAGE", "no placeholder here")
	_, err := Load()
	require.Error(t, err)
}

func TestPoolsMap(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	m := cfg.PoolsMap()
	require.Contains(t, m, "PERSON")
	require.Contains(t, m, "ORGANIZATION")
	require.Contains(t, m, "LOCATION")
}
