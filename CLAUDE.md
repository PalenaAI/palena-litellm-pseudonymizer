# palena-litellm-pseudonymizer

## Purpose

HTTP guardrail service that plugs into the **LiteLLM proxy** via its
`generic_guardrail_api` provider. On the pre-call phase it detects
entity names (people, organizations, locations) via Presidio and
substitutes each with a consistent fictional pseudonym so the upstream
LLM never sees real names. On the post-call phase it reverses the
substitution in the LLM's response so the client sees real names again.

This is a **Go port** of the existing Python
[`palena-guardrail-pseudonymizer`](../palena-guardrail-pseudonymizer/)
service, redesigned around LiteLLM's HTTP guardrail contract instead of
LiteLLM's in-process `CustomGuardrail` interface.

## Package identity

- **Name:** `palena-litellm-pseudonymizer`
  (Go module `github.com/bitkaio/palena-litellm-pseudonymizer`; the local
  checkout folder is `palena-litellm-pseudonymizer-service` only to avoid a
  name clash with a sibling directory.)
- **Language:** Go (≥ 1.22)
- **License:** Apache-2.0
- **Container image:** `ghcr.io/palenaai/palena-litellm-pseudonymizer`

## Tech stack

| Concern | Choice | Why |
|---|---|---|
| HTTP framework | `github.com/go-chi/chi/v5` on stdlib `net/http` | Small, well-maintained, idiomatic |
| Redis client | `github.com/redis/go-redis/v9` | Canonical Go Redis client |
| HTTP client | stdlib `net/http` + timeouts | No hidden deps |
| Config | `github.com/kelseyhightower/envconfig` | Env-first, matches sister repos' conventions |
| Structured logging | `log/slog` (stdlib) | Std since Go 1.21, no third-party dep |
| Testing | stdlib `testing` + `testify/require` | Familiar, no ceremony |
| Redis test double | `github.com/alicebob/miniredis/v2` | In-process, no container in CI |
| HTTP test double | `httptest.NewServer` + `go-httpretty` where useful | Standard |

**No CGO.** The binary must build with `CGO_ENABLED=0` for a scratch image.

## Repository layout

```text
palena-litellm-pseudonymizer-service/
├── CLAUDE.md                  # this file
├── README.md                  # human-facing overview
├── LICENSE
├── Dockerfile
├── Makefile                   # build/test/lint/image targets
├── go.mod / go.sum
├── cmd/
│   └── server/
│       └── main.go            # entry point
├── internal/
│   ├── config/                # env-driven config + validation
│   ├── httpapi/               # chi router, handlers, request/response types
│   ├── pseudonymizer/         # orchestration (BaseGuardrail-equivalent)
│   ├── text/                  # replacer, pools, streaming holdback
│   ├── image/                 # image PII flow
│   ├── presidio/              # analyzer + image redactor clients
│   ├── mapping/               # Redis-backed session mapping
│   ├── session/               # session-id derivation from LiteLLM request
│   └── audit/                 # counter helpers (no-PII invariant enforced)
├── context/                   # design/agent-context docs — READ THESE FIRST
│   ├── PROTOCOL.md
│   ├── PSEUDONYMIZATION_ALGORITHM.md
│   ├── SESSION_AND_STATE.md
│   ├── IMAGE_PIPELINE.md
│   ├── CONFIG.md
│   ├── FAILURE_MODES.md
│   ├── MIGRATION.md
│   └── IMPLEMENTATION_PLAN.md
├── docs/                      # VitePress user-facing documentation SITE
│   └── .vitepress/            # (published; not agent context — see context/)
├── deploy/
│   ├── presidio/              # Presidio configs (NER-ORG + deny-list)
│   └── helm/                  # Helm chart
└── testdata/                  # golden fixtures for text pipeline tests
```

**`context/` holds the design docs (agent context); `docs/` is the published
VitePress site for end users.** Keep them distinct: deep design rationale and
implementation contracts live in `context/`; user-facing how-to lives in
`docs/`. Do not reference `context/` from README or the published site.

**Read `context/IMPLEMENTATION_PLAN.md` before writing code.** It sequences the
milestones (M1 text-only, M2 images, M3 streaming, M4 packaging) and lists
the exact files each milestone touches.

## HTTP contract we serve

The service exposes exactly one endpoint that LiteLLM calls both before
and after every guarded LLM request:

```text
POST /beta/litellm_basic_guardrail_api
Content-Type: application/json
```

Request body is a `GenericGuardrailAPIRequest`; response is a
`GenericGuardrailAPIResponse` with `action ∈ {BLOCKED, NONE, GUARDRAIL_INTERVENED}`.
Full schema, examples, and per-field semantics are in [PROTOCOL.md](context/PROTOCOL.md).

We also serve:

- `GET /healthz` — liveness (returns 200 as long as the process is up)
- `GET /readyz` — readiness (checks Redis + Presidio Analyzer + Presidio Image Redactor)
- `GET /metrics` — Prometheus (via `promhttp`)

## Streaming assumption

The service is designed against the **LiteLLM streaming-transform
extension** to `generic_guardrail_api` (see the upstream PR tracked in
[IMPLEMENTATION_PLAN.md](context/IMPLEMENTATION_PLAN.md)). Streaming mode is
opted into via `additional_provider_specific_params.streaming_transform_mode = "incremental_diff"`
and the service returns `stream_holdback_chars` per choice so LiteLLM
buffers partial pseudonyms until they fully arrive. **Do not implement
custom streaming HTTP semantics** — use the same endpoint LiteLLM already
calls; the framework handles the diff/emit logic.

Until the upstream PR merges, streaming responses ship pseudonyms
unmodified to the client and rely on the sister Python `CustomGuardrail`
adapter for reversal. See [FAILURE_MODES.md](context/FAILURE_MODES.md) for
the graceful-degradation contract.

## Rules

- **NEVER** log or include real ↔ pseudonym mappings in logs, metrics,
  audit metadata, error messages, or Prometheus labels. Counters and
  entity-type labels only. This is enforced by the `internal/audit`
  package and reviewed on every PR.
- **Presidio is external.** No local ML models; every detection is an
  HTTP round-trip. If Presidio is unreachable on pre-call → BLOCK
  (fail-closed). On post-call → return `action: NONE` with a log line
  (best-effort reversal).
- **Redis is external.** Same fail-closed rule on pre-call.
- **Session mappings are namespaced** by `session_id`. See
  [SESSION_AND_STATE.md](context/SESSION_AND_STATE.md) for the derivation
  chain.
- **1:1 length contract** for multimodal in v1. If a transform would
  need to change the length of `texts[]` or `images[]`, return
  `action: BLOCKED` with a clear reason.
- **Longer-first replacement** to prevent "John Mueller" being clobbered
  by an earlier "John" substitution.
- **Fail on `CGO_ENABLED=1`.** The CI build must fail if CGO leaks in.
- **No documents in v1.** The `structured_messages[*].content` entries
  with `type: document` and PDFs delivered as `images` entries are
  rejected with `action: BLOCKED` and reason
  `documents_not_supported_in_v1`. Documents are on the v2 roadmap only.
- **All CRUD on the mapping goes through `internal/mapping`.** Handlers
  never touch Redis directly.

## Testing conventions

- Table-driven tests. One `TestXxx` per subject, subcases via
  `t.Run(name, func(t *testing.T){...})`.
- **Do not use mocks for the mapping store.** Use `miniredis` — it
  covers the surface we care about (hash + TTL) and catches subtle
  serialization bugs mocks would miss.
- Presidio HTTP clients: `httptest.NewServer` returning canned JSON.
  Golden fixtures live in `testdata/presidio/`.
- Text replacer: table-driven with adversarial inputs — possessives,
  case variants, prefix collisions, unicode apostrophes. Copy the Python
  suite's cases as ground truth.
- Streaming holdback: property-style tests — for any random buffer +
  reverse-map, `feed(a) + feed(b) + flush() == replacer.replace(a+b, map)`.
- Coverage floor: 80% on `internal/text`, `internal/mapping`,
  `internal/pseudonymizer`, `internal/session`. Enforce in `Makefile`.

## Make targets

```text
make build         # go build ./cmd/server
make test          # go test ./... -race -count=1
make lint          # golangci-lint run
make cover         # coverage HTML + floor check
make image         # docker build
make run           # runs against docker-compose fixtures (redis + presidio)
```

## Working conventions

- Conventional commits: `feat(text): support unicode apostrophe in replacer`.
- No `git commit` / `git push` / `git rebase` from the agent — user handles.
- Do not add dependencies beyond those listed in "Tech stack" above
  without a note in the PR description explaining why.
- Do not silently drop error paths. Every returned error must either
  bubble up or be logged with structured context (`slog.Error(msg,
  "err", err, "session_id", sid)`).
