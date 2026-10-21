// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package ner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
)

func TestDetect_SplitsIntoBatchesAndKeepsOrder(t *testing.T) {
	var batches [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/detect", r.URL.Path)
		var req detectRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, []string{"person"}, req.Labels)
		require.InDelta(t, 0.9, req.Threshold, 1e-9)
		batches = append(batches, req.Texts)
		res := detectResponse{}
		for _, txt := range req.Texts {
			res.Results = append(res.Results, []Span{{Label: "person", Start: 0, End: len(txt), Score: 0.95}})
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, BatchSize: 2})
	got, err := c.Detect(context.Background(), []string{"a", "bb", "ccc"}, []string{"person"}, 0.9)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"a", "bb"}, {"ccc"}}, batches)
	require.Len(t, got, 3)
	require.Equal(t, 3, got[2][0].End, "results stay parallel to the input")
}

func TestDetect_ResultCountMismatchIsMalformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[[]]}`))
	}))
	defer srv.Close()
	_, err := New(Config{BaseURL: srv.URL}).Detect(context.Background(), []string{"a", "b"}, []string{"person"}, 0.9)
	require.ErrorIs(t, err, errs.ErrNERMalformed)
}

func TestDetect_ErrorStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "text too long", http.StatusRequestEntityTooLarge)
	}))
	defer srv.Close()
	_, err := New(Config{BaseURL: srv.URL}).Detect(context.Background(), []string{"a"}, []string{"person"}, 0.9)
	require.ErrorIs(t, err, errs.ErrNERUnavailable)
}

func TestDetect_UnreachableFailsClosed(t *testing.T) {
	_, err := New(Config{BaseURL: "http://127.0.0.1:1"}).Detect(context.Background(), []string{"a"}, []string{"person"}, 0.9)
	require.ErrorIs(t, err, errs.ErrNERUnavailable)
}

func TestPing_NotReadyUntilModelLoaded(t *testing.T) {
	ready := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL})
	require.ErrorIs(t, c.Ping(context.Background()), errs.ErrNERUnavailable)
	ready = true
	require.NoError(t, c.Ping(context.Background()))
}
