// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

// Package config loads env-driven configuration and validates it
// hard on startup. See context/CONFIG.md for the field list and rules.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"

	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/language"
	"github.com/bitkaio/palena-litellm-pseudonymizer/internal/text"
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

	// Detection engine: "presidio" analyzes every entity type with Presidio
	// (spaCy NER per language); "ner" takes PERSON (and any other type in
	// NER_ENTITY_LABELS) from the NER service — one multilingual span model,
	// no language detection — and only the pattern types (email, phone,
	// IBAN, card, IP, …) from Presidio, always in PRESIDIO_LANGUAGE.
	DetectionEngine string `envconfig:"DETECTION_ENGINE" default:"presidio"`

	// NER service (DETECTION_ENGINE=ner).
	NERURL     string        `envconfig:"NER_URL" default:"http://ner:3000"`
	NERTimeout time.Duration `envconfig:"NER_TIMEOUT_SECONDS" default:"10s"`
	// NERThreshold is the minimum span confidence; ENTITY_SCORE_THRESHOLD
	// overrides it per entity type.
	NERThreshold float64 `envconfig:"NER_THRESHOLD" default:"0.9"`
	// NEREntityLabels maps entity types to model labels:
	// "PERSON:person|full_name|first_name|last_name,ORGANIZATION:organization".
	// A type is taken from the NER service only when it is also in
	// PRESIDIO_ENTITIES. ORGANIZATION via the zero-shot "organization" label
	// is experimental (not benchmarked).
	NEREntityLabels []string `envconfig:"NER_ENTITY_LABELS" default:"PERSON:person|full_name|first_name|last_name,ORGANIZATION:organization"`
	// Each line (a Wäg joined scan puts one message per line) is analyzed
	// on its own; lines longer than NER_CHUNK_CHARS characters are cut into
	// windows overlapping by NER_CHUNK_OVERLAP characters.
	NERChunkChars   int `envconfig:"NER_CHUNK_CHARS" default:"1000"`
	NERChunkOverlap int `envconfig:"NER_CHUNK_OVERLAP" default:"150"`
	// NERBatchSize caps the texts sent to the NER service per HTTP call.
	NERBatchSize int `envconfig:"NER_BATCH_SIZE" default:"16"`

	// Presidio
	AnalyzerURL       string        `envconfig:"PRESIDIO_ANALYZER_URL" default:"http://presidio-analyzer:5001"`
	ImageRedactorURL  string        `envconfig:"PRESIDIO_IMAGE_REDACTOR_URL" default:"http://presidio-image-redactor:5003"`
	PresidioTimeout   time.Duration `envconfig:"PRESIDIO_TIMEOUT_SECONDS" default:"10s"`
	PresidioReadiness time.Duration `envconfig:"PRESIDIO_READINESS_TIMEOUT_SECONDS" default:"2s"`
	ScoreThreshold    float64       `envconfig:"PRESIDIO_SCORE_THRESHOLD" default:"0.7"`
	// EntityScoreThreshold overrides the threshold per entity type:
	// "PHONE_NUMBER:0.4,EMAIL_ADDRESS:0.9". Presidio's phone recognizer scores
	// every match 0.4, so under the 0.7 default no phone number would ever be
	// masked; the default lowers it for that type only.
	EntityScoreThreshold []string `envconfig:"ENTITY_SCORE_THRESHOLD" default:"PHONE_NUMBER:0.4"`
	// LOCATION is intentionally NOT in the default set: pseudonymizing a
	// city (Paris -> Riverside) leaks a wrong geography the model then
	// reasons from ("which Riverside do you mean?", wrong weather/currency).
	// Operators who need it can add LOCATION back via PRESIDIO_ENTITIES.
	Entities []string `envconfig:"PRESIDIO_ENTITIES" default:"PERSON,ORGANIZATION"`
	Language string   `envconfig:"PRESIDIO_LANGUAGE" default:"en"`
	// Languages, when set, replaces Language with a list of ISO 639-1 codes:
	// each text is classified among them and analyzed in its own language
	// (the first is the fallback for text too short to classify). Presidio
	// needs an NLP model loaded for every listed language.
	Languages []string `envconfig:"PRESIDIO_LANGUAGES"`

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

	// AllowList is a set of terms that are NEVER pseudonymized even when
	// Presidio detects them — your own brand/product names, public figures,
	// and words Presidio over-tags ("SSN" as an ORGANIZATION, etc.). Matched
	// case-insensitively against the detected span text. The symmetric
	// counterpart to the organization deny-list.
	AllowList []string `envconfig:"ALLOW_LIST"`
	// AllowListBuiltin adds text.BuiltinAllowList (common product, platform
	// and tool names that are not also people's names) to ALLOW_LIST.
	AllowListBuiltin bool `envconfig:"ALLOW_LIST_BUILTIN" default:"true"`
	// DenyLists are terms always pseudonymized as an entity type, as JSON:
	// [{"entity":"PERSON","terms":["Project Falcon"]}]. Only the types the
	// NER engine serves are matched by the service; every other type is
	// matched by Presidio's deny-list recognizers (the chart renders both
	// from presidio.analyzer.denyLists).
	DenyLists string `envconfig:"DENY_LISTS"`

	// DeterministicSecret, when set, switches POOL assignment from a
	// per-session counter to a keyed HMAC of the real value: the same real
	// name maps to the same pool pseudonym across every session (stable,
	// reproducible pseudonyms). Reversal still uses the session store. Empty
	// (default) keeps the sequential, per-session pool assignment.
	// Token-strategy entities are unaffected. Treat this like a secret.
	DeterministicSecret string `envconfig:"DETERMINISTIC_SECRET"`

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

// AnalyzeLanguages is the validated list of languages Presidio analyzes in:
// PRESIDIO_LANGUAGES when set, else PRESIDIO_LANGUAGE.
func (c *Config) AnalyzeLanguages() []string {
	codes := c.Languages
	if len(codes) == 0 {
		codes = []string{c.Language}
	}
	out, _ := language.Parse(codes) // validated in Validate
	return out
}

// EntityThresholds parses ENTITY_SCORE_THRESHOLD ("TYPE:0.4,…") into a map.
// Invalid entries are surfaced by Validate, not here.
func (c *Config) EntityThresholds() map[string]float64 {
	out := map[string]float64{}
	for _, pair := range c.EntityScoreThreshold {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if !ok {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		out[strings.TrimSpace(k)] = f
	}
	return out
}

// MinScoreThreshold is the lowest threshold any entity type uses: the one
// Presidio is asked for, so a per-type override below the global one still
// gets its matches back. The text handler applies each type's own.
func (c *Config) MinScoreThreshold() float64 {
	m := c.ScoreThreshold
	for _, f := range c.EntityThresholds() {
		m = min(m, f)
	}
	return m
}

// AllowListSet returns the allow-list (ALLOW_LIST, plus the built-in list
// unless ALLOW_LIST_BUILTIN=false) as a set of lower-cased, trimmed terms for
// O(1) case-insensitive lookup. Empty entries are dropped.
func (c *Config) AllowListSet() map[string]struct{} {
	terms := c.AllowList
	if c.AllowListBuiltin {
		terms = append(append([]string{}, terms...), text.BuiltinAllowList...)
	}
	out := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		t := strings.ToLower(strings.TrimSpace(term))
		if t != "" {
			out[t] = struct{}{}
		}
	}
	return out
}

// DenyListEntries parses DENY_LISTS. Invalid JSON is surfaced by Validate;
// here it yields no lists.
func (c *Config) DenyListEntries() []text.DenyList {
	var out []text.DenyList
	if strings.TrimSpace(c.DenyLists) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(c.DenyLists), &out); err != nil {
		return nil
	}
	return out
}

// NERLabels parses NER_ENTITY_LABELS into model label → entity type.
// Invalid entries are surfaced by Validate, not here.
func (c *Config) NERLabels() map[string]string {
	out := map[string]string{}
	for _, pair := range c.NEREntityLabels {
		typ, labels, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if !ok {
			continue
		}
		for _, l := range strings.Split(labels, "|") {
			if l = strings.TrimSpace(l); l != "" {
				out[l] = strings.TrimSpace(typ)
			}
		}
	}
	return out
}

// Validate — matches context/CONFIG.md § "Validation rules".
func (c *Config) Validate() error {
	if _, err := url.Parse(c.AnalyzerURL); err != nil {
		return fmt.Errorf("invalid PRESIDIO_ANALYZER_URL: %w", err)
	}
	if strings.TrimSpace(c.DenyLists) != "" {
		var lists []text.DenyList
		if err := json.Unmarshal([]byte(c.DenyLists), &lists); err != nil {
			return fmt.Errorf("invalid DENY_LISTS: must be a JSON list of {entity, terms}: %w", err)
		}
		for _, l := range lists {
			if strings.TrimSpace(l.Entity) == "" {
				return errors.New("DENY_LISTS entry without an entity")
			}
		}
	}
	switch c.DetectionEngine {
	case "presidio":
	case "ner":
		if u, err := url.Parse(c.NERURL); err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid NER_URL %q: must be an absolute http(s) URL", c.NERURL)
		}
		if c.NERThreshold < 0 || c.NERThreshold > 1 {
			return errors.New("NER_THRESHOLD must be in [0.0, 1.0]")
		}
		if c.NERChunkChars < 100 {
			return errors.New("NER_CHUNK_CHARS must be >= 100")
		}
		if c.NERChunkOverlap < 0 || c.NERChunkOverlap >= c.NERChunkChars/2 {
			return errors.New("NER_CHUNK_OVERLAP must be >= 0 and below NER_CHUNK_CHARS / 2")
		}
		if c.NERBatchSize < 1 {
			return errors.New("NER_BATCH_SIZE must be >= 1")
		}
		for _, pair := range c.NEREntityLabels {
			p := strings.TrimSpace(pair)
			if p == "" {
				continue
			}
			typ, labels, ok := strings.Cut(p, ":")
			if !ok || strings.TrimSpace(typ) == "" || strings.Trim(labels, "| ") == "" {
				return fmt.Errorf("NER_ENTITY_LABELS entry %q must be TYPE:label|label", p)
			}
		}
	default:
		return fmt.Errorf("DETECTION_ENGINE must be presidio|ner, got %q", c.DetectionEngine)
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
	for _, pair := range c.EntityScoreThreshold {
		p := strings.TrimSpace(pair)
		if p == "" {
			continue
		}
		k, v, ok := strings.Cut(p, ":")
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if !ok || strings.TrimSpace(k) == "" || err != nil || f < 0 || f > 1 {
			return fmt.Errorf("ENTITY_SCORE_THRESHOLD entry %q must be TYPE:score with score in [0.0, 1.0]", p)
		}
	}
	if _, err := language.Parse(append([]string{c.Language}, c.Languages...)); err != nil {
		return fmt.Errorf("PRESIDIO_LANGUAGE/PRESIDIO_LANGUAGES: %w", err)
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
