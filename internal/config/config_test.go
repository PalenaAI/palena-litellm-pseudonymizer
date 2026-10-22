// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/text"
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

func TestLoad_Languages(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_PRESIDIO_LANGUAGES", "de, EN,fr")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"de", "en", "fr"}, cfg.AnalyzeLanguages())
}

func TestLoad_LanguageFallsBackToSingle(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_PRESIDIO_LANGUAGE", "it")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"it"}, cfg.AnalyzeLanguages())
}

func TestLoad_BadLanguage(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_PRESIDIO_LANGUAGES", "en,klingon")
	_, err := Load()
	require.ErrorContains(t, err, "klingon")
}

// Presidio's phone recognizer scores 0.4: the default override keeps phone
// numbers maskable under the 0.7 global threshold.
func TestLoad_EntityThresholds(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"PHONE_NUMBER": 0.4}, cfg.EntityThresholds())
	require.InDelta(t, 0.4, cfg.MinScoreThreshold(), 1e-9)

	t.Setenv("PALENA_PSEUDONYMIZER_ENTITY_SCORE_THRESHOLD", "PHONE_NUMBER:1.5")
	_, err = Load()
	require.ErrorContains(t, err, "ENTITY_SCORE_THRESHOLD")
}

func TestLoad_DetectionEngineDefaultsToPresidio(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "presidio", cfg.DetectionEngine)
}

func TestLoad_NEREngine(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_DETECTION_ENGINE", "ner")
	t.Setenv("PALENA_PSEUDONYMIZER_NER_URL", "http://ner:3000")
	cfg, err := Load()
	require.NoError(t, err)
	require.InDelta(t, 0.9, cfg.NERThreshold, 1e-9)
	require.Equal(t, map[string]string{
		"person": "PERSON", "full_name": "PERSON", "first_name": "PERSON", "last_name": "PERSON",
		"organization": "ORGANIZATION",
	}, cfg.NERLabels())
}

func TestLoad_NEREngineRejectsBadSettings(t *testing.T) {
	for env, val := range map[string]string{
		"PALENA_PSEUDONYMIZER_DETECTION_ENGINE":  "spacy",
		"PALENA_PSEUDONYMIZER_NER_URL":           "ner:3000",
		"PALENA_PSEUDONYMIZER_NER_THRESHOLD":     "1.2",
		"PALENA_PSEUDONYMIZER_NER_CHUNK_CHARS":   "50",
		"PALENA_PSEUDONYMIZER_NER_CHUNK_OVERLAP": "600",
		"PALENA_PSEUDONYMIZER_NER_ENTITY_LABELS": "PERSON",
		"PALENA_PSEUDONYMIZER_NER_BATCH_SIZE":    "0",
	} {
		t.Run(env, func(t *testing.T) {
			if env != "PALENA_PSEUDONYMIZER_DETECTION_ENGINE" {
				t.Setenv("PALENA_PSEUDONYMIZER_DETECTION_ENGINE", "ner")
			}
			t.Setenv(env, val)
			_, err := Load()
			require.Error(t, err)
		})
	}
}

func TestAllowListSet_MergesBuiltin(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_ALLOW_LIST", "Navique, Alexa")
	cfg, err := Load()
	require.NoError(t, err)
	set := cfg.AllowListSet()
	require.Contains(t, set, "navique")
	require.Contains(t, set, "alexa")
	require.Contains(t, set, "proton pass", "built-in list is on by default")

	t.Setenv("PALENA_PSEUDONYMIZER_ALLOW_LIST_BUILTIN", "false")
	cfg, err = Load()
	require.NoError(t, err)
	require.Equal(t, map[string]struct{}{"navique": {}, "alexa": {}}, cfg.AllowListSet())
}

func TestLoad_DenyLists(t *testing.T) {
	t.Setenv("PALENA_PSEUDONYMIZER_DENY_LISTS", `[{"entity":"PERSON","terms":["Project Falcon","Acme, Inc."]}]`)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []text.DenyList{{Entity: "PERSON", Terms: []string{"Project Falcon", "Acme, Inc."}}}, cfg.DenyListEntries())

	t.Setenv("PALENA_PSEUDONYMIZER_DENY_LISTS", `PERSON:Falcon`)
	_, err = Load()
	require.ErrorContains(t, err, "DENY_LISTS")
}
