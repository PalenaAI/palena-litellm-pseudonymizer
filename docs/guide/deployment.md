# Deploy with Helm

The chart deploys the guardrail service and, optionally, its Redis and Presidio
dependencies.

## Install

```bash
helm install pseudonymizer \
  ./deploy/helm/palena-litellm-pseudonymizer \
  --namespace guardrails --create-namespace
```

By default this brings up the guardrail plus a single-instance Redis and both
Presidio services — a self-contained stack for a proof of concept. Helm prints
the in-cluster URL and a ready-to-paste LiteLLM guardrail block on install.

## Production: bring your own dependencies

Disable the bundled Redis and Presidio and point at managed instances:

```yaml
redis:
  enabled: false
  url: redis://my-managed-redis:6379/0

presidio:
  analyzer:
    enabled: false
    url: http://my-presidio-analyzer:3000
  imageRedactor:
    enabled: false
    url: http://my-image-redactor:3000
```

## NER service

With `config.detectionEngine: ner` (the default) the chart also deploys the NER
service, `ghcr.io/palenaai/palena-ner:<appVersion>`: GLiNER2-PII on CPU with the
model baked into the image, so it needs no network access at runtime. It takes
about 2 Gi of memory (limit 3 Gi) and 10–30 s to load; `/health` answers 503
until then, and the guardrail's `/readyz` waits for it. Presidio then only runs
its pattern recognizers, in `config.language`, so the analyzer stays on the
stock English image whatever `config.languages` says.

```yaml
ner:
  replicas: 2
  torchThreads: 2          # match resources.limits.cpu
  resources:
    requests: { cpu: "1", memory: 2Gi }
    limits: { cpu: "2", memory: 3Gi }
```

To use your own NER service, set `ner.enabled: false` and `ner.url`.
`config.detectionEngine: presidio` keeps the previous behaviour (spaCy per
language, no NER service).

## Organization names

Add your known company names for high-precision masking (see
[Organization detection](/guide/organization-detection)):

```yaml
presidio:
  analyzer:
    organizations:
      - Novartis
      - Roche
```

## Common values

| Key | Default | Purpose |
| --- | --- | --- |
| `replicaCount` | `2` | Guardrail replicas. |
| `image.tag` | chart appVersion | Service image tag. |
| `config.detectionEngine` | `ner` | `ner` (NER service for names) or `presidio` (spaCy per language). |
| `config.entities` | `PERSON` | Detected entity types. |
| `config.languages` | `[]` (= `config.language`, `en`) | `presidio` engine only: languages to analyze in; several switch to the multilingual analyzer image. |
| `config.entityScoreThreshold` | `PHONE_NUMBER:0.4` | Per-type minimum score. |
| `config.allowList` | `""` | Terms never pseudonymized. |
| `config.allowListBuiltin` | `true` | Add the built-in product/tool name list. |
| `ner.threshold` | `0.9` | Minimum span confidence. |
| `ner.chunkChars` / `ner.chunkOverlap` | `1000` / `150` | Window size for long lines. |
| `apiKey.enabled` | `false` | Require an `x-api-key` header. |
| `redis.enabled` | `true` | Bundle Redis. |
| `presidio.analyzer.nerOrganization` | `true` | spaCy org detection. |
| `presidio.analyzer.organizations` | `[]` | Exact-match org deny-list. |
| `presidio.analyzer.denyLists` | `[]` | Exact-match deny-lists for any entity type. |
| `presidio.analyzer.multilingualImage` | `ghcr.io/palenaai/palena-presidio-analyzer:<appVersion>` | Analyzer used for anything but English alone. |
| `autoscaling.enabled` | `false` | HPA on CPU. |
| `serviceMonitor.enabled` | `false` | Prometheus Operator scrape. |

The full list lives in the chart's `values.yaml`.

## Security

Set an API key before exposing the service beyond the proxy:

```yaml
apiKey:
  enabled: true
  value: "a-long-random-secret"       # or reference an existing Secret
  # existingSecret: my-secret
  # existingSecretKey: api-key
```

## Uninstall

```bash
helm uninstall pseudonymizer --namespace guardrails
```
