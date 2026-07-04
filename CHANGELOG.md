# Changelog

All notable changes to the Palena Pseudonymizer are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Optional structured-PII pseudonymization** — any entity type Presidio
  detects (`CREDIT_CARD`, `US_SSN`, `IBAN_CODE`, `EMAIL_ADDRESS`,
  `PHONE_NUMBER`, custom IDs, …) can now be enabled via
  `PRESIDIO_ENTITIES`. Two substitution strategies:
  - **`pool`** — realistic fictional value (unchanged behaviour for nominal
    identities: `PERSON` / `ORGANIZATION` / `LOCATION`).
  - **`token`** — a consistent reversible placeholder (`<CREDIT_CARD_1>`),
    the new default for non-nominal types. Hides the value, signals the
    redaction to the model, and reverses to the exact original.
  - Configurable via `ENTITY_STRATEGY_DEFAULT` and per-type
    `ENTITY_STRATEGY` overrides (`"PHONE_NUMBER:pool,CREDIT_CARD:token"`);
    surfaced in the Helm chart. Nominal types default to `pool` regardless
    of the default, so existing deployments are unaffected.

### Fixed

- Token pseudonyms are no longer case-folded by the case-preserving replacer
  in either direction (forward masking or response reversal), so a token like
  `<EMAIL_ADDRESS_1>` stays verbatim and reverses to the exact original value.

## [0.1.0] - 2026-06-03

Initial release.

### Added

- **LiteLLM `generic_guardrail_api` HTTP service** exposing
  `POST /beta/litellm_basic_guardrail_api`, plus `/healthz`, `/readyz`, and
  `/metrics`. Handles both the pre-call (`input_type: request`) and post-call
  (`input_type: response`) phases at one endpoint.
- **Text pseudonymization** — detects `PERSON` / `ORGANIZATION` entities via
  Presidio Analyzer and substitutes each with a consistent fictional pseudonym
  from configurable pools; reverses the substitution in the response.
  - Case-insensitive matching with case-preserving output, possessive-safe,
    longest-match-first, word-boundary aware (RE2, no lookaround).
  - **Gender-neutral default person pool** so a masked name never leaks a wrong
    gender into the model's reasoning.
  - **`LOCATION` off by default** to avoid geography drift (a masked city makes
    the model reason from the wrong place).
- **Person-name decomposition** — mapping "Alice Johnson → Jordan Avery" also
  records "Alice → Jordan" and "Johnson → Avery", so bare first/last-name
  references stay consistent and reverse correctly. Toggle with
  `PALENA_PSEUDONYMIZER_DECOMPOSE_PERSON_NAMES`.
- **Streaming reversal** via LiteLLM's `streaming_transform_mode:
  incremental_diff`. The service returns `stream_holdback_chars` per choice so
  the framework never emits a partial pseudonym mid-stream.
- **Image PII** — detects and redacts PII in image attachments via Presidio
  Image Redactor (`redact` / `block` / `passthrough`), sharing OCR'd names with
  the text session mapping.
- **Session-scoped Redis mapping** — real↔pseudonym mappings are namespaced by
  a derived `session_id` (metadata → end-user → trace id → call id), stored via
  an atomic Lua merge (existing assignments win), with a refreshing TTL.
- **Fail-closed semantics** — if Presidio or Redis is unreachable on the way to
  the model, the request is blocked; post-call reversal failures degrade to a
  passthrough so a successful response is never lost.
- **No-PII audit invariant** — structured logs and Prometheus metrics carry
  counts and entity-type labels only, never real names or pseudonyms; session
  ids appear only as short hashes.
- **Helm chart** (`deploy/helm/palena-litellm-pseudonymizer-service`) deploying
  the service with optional bundled Redis and Presidio. Organization detection
  is on by default (spaCy NER) with an optional exact-match deny-list of known
  company names for high precision.
- **VitePress documentation site** under `docs/` (getting started,
  configuration, organization detection, deployment, LiteLLM integration, HTTP
  protocol, environment reference).
- **CI/CD** — lint + race tests, Semgrep, Hadolint, zizmor, govulncheck,
  dependency review, conventional-commit and license-header checks, Trivy image
  scanning with SBOM, cosign keyless signing, SLSA Level 3 provenance, and
  OpenSSF Scorecard.

[Unreleased]: https://github.com/PalenaAI/palena-litellm-pseudonymizer/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/PalenaAI/palena-litellm-pseudonymizer/releases/tag/v0.1.0
