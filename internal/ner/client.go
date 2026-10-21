// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

// Package ner is the HTTP client for the NER service (deploy/ner): a GLiNER2
// span model that detects names in any language without a language hint.
// Detection failures fail closed like Presidio's (context/FAILURE_MODES.md).
package ner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
)

// Span is one detection inside one input text. Start/End count characters
// (Unicode code points, Python string indices), not bytes.
type Span struct {
	Label string  `json:"label"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
}

// Config configures the client.
type Config struct {
	BaseURL string
	Timeout time.Duration
	// BatchSize caps the texts sent per HTTP call; larger inputs are split
	// into several calls.
	BatchSize int
}

// Client calls POST /v1/detect and GET /health.
type Client struct {
	baseURL   string
	batchSize int
	http      *http.Client
}

// New constructs a client.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 16
	}
	return &Client{
		baseURL:   strings.TrimRight(cfg.BaseURL, "/"),
		batchSize: cfg.BatchSize,
		http:      &http.Client{Timeout: cfg.Timeout},
	}
}

type detectRequest struct {
	Texts     []string `json:"texts"`
	Labels    []string `json:"labels"`
	Threshold float64  `json:"threshold"`
}

type detectResponse struct {
	Results [][]Span `json:"results"`
}

// Detect returns, for every text, the spans of the given labels scoring at
// least threshold. The result is parallel to texts.
//
// Errors:
//   - errs.ErrNERUnavailable — connect / timeout / non-2xx
//   - errs.ErrNERMalformed   — undecodable body or a result count/offset that
//     does not match the request
func (c *Client) Detect(ctx context.Context, texts, labels []string, threshold float64) ([][]Span, error) {
	out := make([][]Span, 0, len(texts))
	for start := 0; start < len(texts); start += c.batchSize {
		end := min(start+c.batchSize, len(texts))
		batch, err := c.detectBatch(ctx, texts[start:end], labels, threshold)
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

func (c *Client) detectBatch(ctx context.Context, texts, labels []string, threshold float64) ([][]Span, error) {
	body, err := json.Marshal(detectRequest{Texts: texts, Labels: labels, Threshold: threshold})
	if err != nil {
		return nil, fmt.Errorf("marshal detect request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/detect", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build detect request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errs.Wrap(errs.ErrNERUnavailable, "detect request")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Any non-2xx is "detection didn't work" — fail closed. The body is
		// the service's own error text, never request content.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, errs.Wrap(errs.ErrNERUnavailable, "detect status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var dr detectResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, errs.Wrap(errs.ErrNERMalformed, "decode detect response")
	}
	if len(dr.Results) != len(texts) {
		return nil, errs.Wrap(errs.ErrNERMalformed, "detect returned %d results for %d texts", len(dr.Results), len(texts))
	}
	return dr.Results, nil
}

// Ping — used by the readiness handler. The service answers 503 until its
// model is loaded.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errs.Wrap(errs.ErrNERUnavailable, "ner health")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errs.Wrap(errs.ErrNERUnavailable, "ner health status %d", resp.StatusCode)
	}
	return nil
}
