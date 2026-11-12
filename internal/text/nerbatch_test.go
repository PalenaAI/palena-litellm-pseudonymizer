// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package text

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/errs"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/ner"
)

// batchNER records every call and can hold calls until released, fail one,
// and report how many ran at once. Safe for concurrent use.
type batchNER struct {
	mu        sync.Mutex
	calls     [][]string
	inFlight  int
	maxFlight int
	holdFrom  int           // calls from this index (0-based) block on release
	release   chan struct{} // nil = never block
	failCall  int           // call index that fails; -1 = none
}

func newBatchNER() *batchNER { return &batchNER{failCall: -1, holdFrom: 1 << 30} }

func (b *batchNER) Detect(ctx context.Context, texts, _ []string, _ float64) ([][]ner.Span, error) {
	b.mu.Lock()
	n := len(b.calls)
	b.calls = append(b.calls, append([]string(nil), texts...))
	b.inFlight++
	b.maxFlight = max(b.maxFlight, b.inFlight)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.inFlight--
		b.mu.Unlock()
	}()

	if n >= b.holdFrom && b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, errs.SendFailure(ctx, ctx.Err(), errs.ErrNERUnavailable, "ner detect", time.Second)
		}
	} else {
		time.Sleep(5 * time.Millisecond) // let parallel calls overlap
	}
	if n == b.failCall {
		return nil, errs.Wrap(errs.ErrNERUnavailable, "boom")
	}
	out := make([][]ner.Span, len(texts))
	for i, t := range texts {
		if j := strings.Index(t, "Name"); j >= 0 {
			out[i] = []ner.Span{{Label: "person", Start: j, End: j + 4, Score: 0.95}}
		}
	}
	return out, nil
}

func (b *batchNER) sent() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var all []string
	for _, c := range b.calls {
		all = append(all, c...)
	}
	return all
}

func batchDetector(stub NERClient, parallel int) *NERDetector {
	return &NERDetector{
		NER: stub, Labels: map[string]string{"person": "PERSON"}, Threshold: 0.9,
		ChunkChars: 1000, ChunkOverlap: 150, BatchSize: 2, Parallel: parallel,
		Cache: NewNERCache(100, time.Hour),
	}
}

func lines(n int) string {
	ls := make([]string, n)
	for i := range ls {
		ls[i] = fmt.Sprintf("line %d with a Name", i)
	}
	return strings.Join(ls, "\n")
}

// Production: a long conversation needed more than the gateway's 60 s, the
// gateway cancelled, and every finished batch was thrown away — so every
// later turn started from zero and was cancelled again.
func TestNERDetector_CancelledRequestKeepsFinishedBatches(t *testing.T) {
	stub := newBatchNER()
	stub.holdFrom, stub.release = 1, make(chan struct{}) // batch 0 answers, batch 1 hangs
	d := batchDetector(stub, 1)
	text := lines(6) // 3 batches of 2

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			stub.mu.Lock()
			n := len(stub.calls)
			stub.mu.Unlock()
			if n >= 2 { // batch 1 is in flight: the caller gives up
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	_, err := d.Detect(ctx, text)
	require.ErrorIs(t, err, errs.ErrCallerCanceled)
	require.Len(t, stub.calls, 2, "nothing new starts after the caller gave up")
	require.Equal(t, 2, d.Cache.Stats().Entries, "the finished batch is cached")

	// Next attempt: only what is left goes out.
	close(stub.release)
	before := len(stub.sent())
	got, err := d.Detect(context.Background(), text)
	require.NoError(t, err)
	require.Len(t, got, 6, "every line's name is reported")
	require.Equal(t, []string{"line 2 with a Name", "line 3 with a Name", "line 4 with a Name", "line 5 with a Name"},
		stub.sent()[before:])
}

func TestNERDetector_AlreadyCancelledSendsNothing(t *testing.T) {
	stub := newBatchNER()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := batchDetector(stub, 2).Detect(ctx, lines(4))
	require.ErrorIs(t, err, errs.ErrCallerCanceled)
	require.Empty(t, stub.sent())
}

func TestNERDetector_ParallelBatchesStayInOrder(t *testing.T) {
	stub := newBatchNER()
	d := batchDetector(stub, 2)
	text := lines(10) // 5 batches of 2
	got, err := d.Detect(context.Background(), text)
	require.NoError(t, err)
	require.Len(t, got, 10)
	lineStart := 0
	for i, line := range strings.Split(text, "\n") {
		require.Equal(t, "Name", got[i].Text)
		require.Equal(t, lineStart+strings.Index(line, "Name"), got[i].Start, "detection %d lies in its own line", i)
		lineStart += len(line) + 1
	}
	require.Equal(t, 2, stub.maxFlight, "at most Parallel calls at once")
	require.Equal(t, 10, d.Cache.Stats().Entries)
}

func TestNERDetector_FailedBatchReportsItsErrorAndKeepsTheOthers(t *testing.T) {
	stub := newBatchNER()
	stub.failCall = 1
	d := batchDetector(stub, 1)
	_, err := d.Detect(context.Background(), lines(6))
	require.ErrorIs(t, err, errs.ErrNERUnavailable, "the real failure, not a cancellation")
	require.NotErrorIs(t, err, errs.ErrCallerCanceled)
	require.Len(t, stub.calls, 2, "no batch starts after a failure")
	require.Equal(t, 2, d.Cache.Stats().Entries, "the batch before the failure is cached")
}

func TestNERDetector_MalformedBatchIsAnError(t *testing.T) {
	d := batchDetector(shortNER{}, 1)
	_, err := d.Detect(context.Background(), lines(2))
	require.ErrorIs(t, err, errs.ErrNERMalformed)
}

type shortNER struct{}

func (shortNER) Detect(context.Context, []string, []string, float64) ([][]ner.Span, error) {
	return [][]ner.Span{nil}, nil // one result for two texts
}
