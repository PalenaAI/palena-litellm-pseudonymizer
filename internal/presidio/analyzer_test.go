// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package presidio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
)

func newAnalyzerServer(t *testing.T, h http.HandlerFunc) *Analyzer {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewAnalyzer(AnalyzerConfig{BaseURL: srv.URL, ScoreThreshold: 0.7})
}

func TestAnalyzer_Success(t *testing.T) {
	a := newAnalyzerServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/analyze", r.URL.Path)
		var body analyzeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Novartis hired Thomas Weber.", body.Text)
		require.Equal(t, "en", body.Language)
		require.Equal(t, 0.7, body.ScoreThreshold)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Detection{
			{EntityType: "ORGANIZATION", Start: 0, End: 8, Score: 0.9},
			{EntityType: "PERSON", Start: 15, End: 27, Score: 0.85},
		})
	})
	got, err := a.Analyze(context.Background(), "Novartis hired Thomas Weber.",
		[]string{"PERSON", "ORGANIZATION"}, "en")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "Novartis", got[0].Text)
	require.Equal(t, "Thomas Weber", got[1].Text)
}

func TestAnalyzer_EmptyInputSkipsCall(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{BaseURL: "http://never-called"})
	got, err := a.Analyze(context.Background(), "", []string{"PERSON"}, "en")
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = a.Analyze(context.Background(), "hello", nil, "en")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestAnalyzer_5xxMappedToUnavailable(t *testing.T) {
	a := newAnalyzerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := a.Analyze(context.Background(), "hi", []string{"PERSON"}, "en")
	require.Error(t, err)
	require.True(t, errors.Is(err, errs.ErrPresidioUnavailable))
}

func TestAnalyzer_MalformedBody(t *testing.T) {
	a := newAnalyzerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{not json")
	})
	_, err := a.Analyze(context.Background(), "hi", []string{"PERSON"}, "en")
	require.Error(t, err)
	require.True(t, errors.Is(err, errs.ErrPresidioMalformed))
}

func TestAnalyzer_ConnectFailure(t *testing.T) {
	a := NewAnalyzer(AnalyzerConfig{BaseURL: "http://127.0.0.1:1"}) // reserved
	_, err := a.Analyze(context.Background(), "hi", []string{"PERSON"}, "en")
	require.Error(t, err)
	require.True(t, errors.Is(err, errs.ErrPresidioUnavailable))
}

func TestAnalyzer_FillsMissingText(t *testing.T) {
	a := newAnalyzerServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"entity_type": "PERSON", "start": 0, "end": 5, "score": 0.9},
		})
	})
	got, err := a.Analyze(context.Background(), "Alice met Bob.", []string{"PERSON"}, "en")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Alice", got[0].Text)
}
