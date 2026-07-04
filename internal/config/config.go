// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

// Package config loads env-driven configuration and validates it
// hard on startup. See context/CONFIG.md for the field list and rules.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config is the parsed shape. Env vars are read with the
// PALENA_PSEUDONYMIZER_ prefix. Flat struct — no nesting — because
// envconfig's nested-struct handling prepends the field name as an
// additional prefix, which we do not want.
type Config struct {
	// HTTP server
	HTTPAddr           string        `envconfig:"HTTP_ADDR" default:":8080"`
	HTTPReadTimeout    time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"30s"`
	HTTPWriteTimeout   time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"30s"`
	HTTPIdleTimeout    time.Duration `envconfig:"HTTP_IDLE_TIMEOUT" default:"120s"`
	HTTPMaxHeaderBytes int           `envconfig:"HTTP_MAX_HEADER_BYTES" default:"1048576"`

	// Presidio
	AnalyzerURL       string        `envconfig:"PRESIDIO_ANALYZER_URL" default:"http://presidio-analyzer:5001"`
	ImageRedactorURL  string        `envconfig:"PRESIDIO_IMAGE_REDACTOR_URL" default:"http://presidio-image-redactor:5003"`
	PresidioTimeout   time.Duration `envconfig:"PRESIDIO_TIMEOUT_SECONDS" default:"10s"`
	PresidioReadiness time.Duration `envconfig:"PRESIDIO_READINESS_TIMEOUT_SECONDS" default:"2s"`
	ScoreThreshold    float64       `envconfig:"PRESIDIO_SCORE_THRESHOLD" default:"0.7"`
	// LOCATION is intentionally NOT in the default set: pseudonymizing a
	// city (Paris -> Riverside) leaks a wrong geography the model then
	// reasons from ("which Riverside do you mean?", wrong weather/currency).
	// Operators who need it can add LOCATION back via PRESIDIO_ENTITIES.
	Entities []string `envconfig:"PRESIDIO_ENTITIES" default:"PERSON,ORGANIZATION"`
	Language string   `envconfig:"PRESIDIO_LANGUAGE" default:"en"`

	// Substitution strategy per entity type. Nominal identities
	// (PERSON/ORGANIZATION/LOCATION) default to "pool" — a realistic
	// fictional name. Structured PII (CREDIT_CARD, US_SSN, IBAN_CODE,
	// EMAIL_ADDRESS, custom IDs, …) defaults to "token" — a consistent,
	// reversible placeholder like <CREDIT_CARD_1>, because a fake name for
	// a card number is meaningless and the model rarely needs the digits.
	//
	// ENTITY_STRATEGY overrides per type: "CREDIT_CARD:token,PHONE_NUMBER:pool".
	// ENTITY_STRATEGY_DEFAULT is used for any enabled type that is neither
	// nominal nor explicitly overridden.
	EntityStrategy        []string `envconfig:"ENTITY_STRATEGY"`
	EntityStrategyDefault string   `envconfig:"ENTITY_STRATEGY_DEFAULT" default:"token"`

	// Redis
	RedisURL       string        `envconfig:"REDIS_URL" default:"redis://redis:6379/0"`
	SessionTTL     time.Duration `envconfig:"REDIS_SESSION_TTL_SECONDS" default:"3600s"`
	RedisKeyPrefix string        `envconfig:"REDIS_KEY_PREFIX" default:"palena:pseudonymizer"`
	RedisTimeout   time.Duration `envconfig:"REDIS_TIMEOUT_SECONDS" default:"2s"`
	RedisPoolSize  int           `envconfig:"REDIS_POOL_SIZE" default:"10"`
	RedisCluster   bool          `envconfig:"REDIS_CLUSTER" default:"false"`

	// Pools
	// PERSON names are deliberately gender-AMBIGUOUS (unisex given names +
	// neutral surnames). A pseudonym carries an implied gender the model
	// reasons from, so a female real name mapped to "Thomas" leaks a wrong
	// "he". Neutral names give the model no strong prior — it says "they"
	// or asks — so the wrong gender never leaks. This needs no gender logic
	// in code; it is purely a curated, operator-overridable default.
	PoolPerson       []string `envconfig:"POOL_PERSON" default:"Jordan Avery,Taylor Morgan,Alex Rivera,Riley Quinn,Casey Bennett,Sam Ellis,Jamie Brooks,Cameron Hayes"`
	PoolOrganization []string `envconfig:"POOL_ORGANIZATION" default:"Acme Corp,Birch Industries,Cedar Systems,Delta Group,Elm Partners,Fern Solutions,Grove Holdings,Hazel Dynamics"`
	// LOCATION pool is unused by default (LOCATION is not in the default
	// entity set) but kept so operators who opt LOCATION back in get a
	// sensible pool without extra config.
	PoolLocation []string `envconfig:"POOL_LOCATION" default:"Riverside,Lakewood,Hillcrest,Meadowbrook,Stonefield,Clearwater"`

	// Session
	SessionMetadataKey string `envconfig:"SESSION_METADATA_KEY" default:"session_id"`

	// Text handling
	// DecomposePersonNames registers first/last-name sub-mappings for
	// multi-token PERSON names so bare first/last references stay
	// consistent with the full-name pseudonym. See CONFIG.md.
	DecomposePersonNames bool `envconfig:"DECOMPOSE_PERSON_NAMES" default:"true"`

	// Non-text content
	PIIAction           string `envconfig:"NON_TEXT_PII_ACTION" default:"redact"`
	BlockMessage        string `envconfig:"NON_TEXT_BLOCK_MESSAGE" default:"This attachment contains personal data ({entity_types}) that cannot be pseudonymized. Please remove sensitive information and try again."`
	MaxImageBytes       int64  `envconfig:"MAX_IMAGE_BYTES" default:"20971520"`
	MaxImagesPerRequest int    `envconfig:"MAX_IMAGES_PER_REQUEST" default:"20"`

	// Runtime
	APIKey          string        `envconfig:"API_KEY"`
	LogLevel        string        `envconfig:"LOG_LEVEL" default:"info"`
	LogFormat       string        `envconfig:"LOG_FORMAT" default:"json"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`
	MaxRequestBytes int64         `envconfig:"MAX_REQUEST_BYTES" default:"33554432"`
}

// Load reads env vars into Config and validates. Fails fast.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("palena_pseudonymizer", &cfg); err != nil {
		return nil, fmt.Errorf("envconfig: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PoolsMap flattens the three pool fields into a map[entityType]list.
func (c *Config) PoolsMap() map[string][]string {
	return map[string][]string{
		"PERSON":       c.PoolPerson,
		"ORGANIZATION": c.PoolOrganization,
		"LOCATION":     c.PoolLocation,
	}
}

// StrategyOverrides parses ENTITY_STRATEGY ("TYPE:strategy,…") into a map.
// Invalid entries are surfaced by Validate, not here.
func (c *Config) StrategyOverrides() map[string]string {
	out := map[string]string{}
	for _, pair := range c.EntityStrategy {
		p := strings.TrimSpace(pair)
		if p == "" {
			continue
		}
		k, v, ok := strings.Cut(p, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.ToLower(strings.TrimSpace(v))
	}
	return out
}

// Validate — matches context/CONFIG.md § "Validation rules".
func (c *Config) Validate() error {
	if _, err := url.Parse(c.AnalyzerURL); err != nil {
		return fmt.Errorf("invalid PRESIDIO_ANALYZER_URL: %w", err)
	}
	if _, err := url.Parse(c.ImageRedactorURL); err != nil {
		return fmt.Errorf("invalid PRESIDIO_IMAGE_REDACTOR_URL: %w", err)
	}
	if _, err := url.Parse(c.RedisURL); err != nil {
		return fmt.Errorf("invalid REDIS_URL: %w", err)
	}
	if c.ScoreThreshold < 0 || c.ScoreThreshold > 1 {
		return errors.New("PRESIDIO_SCORE_THRESHOLD must be in [0.0, 1.0]")
	}
	if len(c.Entities) == 0 {
		return errors.New("PRESIDIO_ENTITIES must be non-empty")
	}
	validStrategy := func(s string) bool { return s == "pool" || s == "token" }
	if !validStrategy(c.EntityStrategyDefault) {
		return fmt.Errorf("ENTITY_STRATEGY_DEFAULT must be pool|token, got %q", c.EntityStrategyDefault)
	}
	for t, s := range c.StrategyOverrides() {
		if !validStrategy(s) {
			return fmt.Errorf("ENTITY_STRATEGY for %q must be pool|token, got %q", t, s)
		}
	}
	switch c.PIIAction {
	case "redact", "block", "passthrough":
	default:
		return fmt.Errorf("NON_TEXT_PII_ACTION must be redact|block|passthrough, got %q", c.PIIAction)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL must be debug|info|warn|error, got %q", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("LOG_FORMAT must be json|text, got %q", c.LogFormat)
	}
	if c.SessionTTL <= 0 {
		return errors.New("REDIS_SESSION_TTL_SECONDS must be > 0")
	}
	if c.RedisPoolSize < 1 {
		return errors.New("REDIS_POOL_SIZE must be >= 1")
	}
	if c.HTTPMaxHeaderBytes <= 0 {
		return errors.New("HTTP_MAX_HEADER_BYTES must be > 0")
	}
	if c.MaxRequestBytes <= 0 {
		return errors.New("MAX_REQUEST_BYTES must be > 0")
	}
	if c.MaxImageBytes <= 0 {
		return errors.New("MAX_IMAGE_BYTES must be > 0")
	}
	if c.MaxImagesPerRequest <= 0 {
		return errors.New("MAX_IMAGES_PER_REQUEST must be > 0")
	}
	if strings.Count(c.BlockMessage, "{entity_types}") != 1 {
		return errors.New("NON_TEXT_BLOCK_MESSAGE must contain exactly one {entity_types} placeholder")
	}
	return nil
}
