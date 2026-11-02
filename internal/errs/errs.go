// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

// Package errs defines sentinel errors + typed wrappers used across the
// pseudonymizer service. The HTTP layer maps these to specific status
// codes per context/FAILURE_MODES.md.
package errs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

var (
	// ErrPresidioUnavailable is returned when Presidio Analyzer /
	// Image Redactor is unreachable, times out, or returns a 5xx.
	// HTTP maps to 502 on pre-call.
	ErrPresidioUnavailable = errors.New("presidio_unreachable")

	// ErrPresidioMalformed is returned when Presidio returns a
	// response we cannot decode. HTTP maps to 502 on pre-call.
	ErrPresidioMalformed = errors.New("presidio_malformed")

	// ErrNERUnavailable is returned when the NER service is unreachable,
	// times out, or answers non-2xx. HTTP maps to 502 on pre-call.
	ErrNERUnavailable = errors.New("ner_unreachable")

	// ErrNERMalformed is returned when the NER service returns a body we
	// cannot decode or that does not match the request. HTTP maps to 502.
	ErrNERMalformed = errors.New("ner_malformed")

	// ErrMappingStoreUnavailable is returned when Redis is unreachable
	// or a Redis command fails. HTTP maps to 502 on pre-call.
	ErrMappingStoreUnavailable = errors.New("mapping_store_unreachable")

	// ErrCallerCanceled is returned when the request that needed a
	// dependency was abandoned by its caller (the gateway's guardrail
	// timeout, a client disconnect) while the dependency call was in
	// flight. The dependency itself may be healthy. Logged as a warning;
	// the response goes nowhere.
	ErrCallerCanceled = errors.New("caller_canceled")

	// ErrSessionDerivationFailed is returned when we cannot derive
	// any session id from the request (bad wiring). HTTP 400.
	ErrSessionDerivationFailed = errors.New("session_id_derivation_failed")

	// ErrRequestTooLarge — HTTP 413.
	ErrRequestTooLarge = errors.New("request_too_large")

	// ErrImageTooLarge — HTTP 413.
	ErrImageTooLarge = errors.New("image_too_large")

	// ErrImageFetchFailed — could not fetch a remote image URL.
	ErrImageFetchFailed = errors.New("image_fetch_failed")
)

// BlockDecision signals that the guardrail decided to block the LLM
// call for a user-visible reason (not an error). The HTTP layer
// converts this to `action: BLOCKED` with the given reason.
type BlockDecision struct {
	Reason string
}

func (b *BlockDecision) Error() string { return b.Reason }

// NewBlock returns a *BlockDecision for the given reason code.
func NewBlock(reason string) *BlockDecision { return &BlockDecision{Reason: reason} }

// AsBlock reports whether err is a *BlockDecision and returns it.
func AsBlock(err error) (*BlockDecision, bool) {
	var b *BlockDecision
	if errors.As(err, &b) {
		return b, true
	}
	return nil, false
}

// Wrap adds context to an error while preserving errors.Is/As chains.
func Wrap(err error, format string, a ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(a, err)...)
}

// SendFailure classifies an error from sending a request to a dependency
// (`what`, e.g. "ner detect"): the caller gave up (ErrCallerCanceled), the
// client timed out after `timeout`, or the dependency could not be reached
// (the transport error is kept for the log). Not-caller failures wrap
// `unavailable`.
func SendFailure(ctx context.Context, err error, unavailable error, what string, timeout time.Duration) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w (%w)", what, ErrCallerCanceled, ctxErr)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return Wrap(unavailable, "%s timed out after %s", what, timeout)
	}
	return Wrap(unavailable, "%s failed: %v", what, err)
}
