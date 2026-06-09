# Environment variables

All settings are prefixed `PALENA_PSEUDONYMIZER_`. The service validates them on
startup and exits on invalid values.

## Runtime

| Variable | Default | Notes |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | Bind address. |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`. |
| `LOG_FORMAT` | `json` | `json` \| `text`. |
| `MAX_REQUEST_BYTES` | `33554432` | Request body cap (32 MiB). |
| `API_KEY` | *(empty)* | Required `x-api-key` value; empty = accept all. |
| `SHUTDOWN_TIMEOUT` | `30s` | Graceful drain period. |

## Presidio

| Variable | Default | Notes |
| --- | --- | --- |
| `PRESIDIO_ANALYZER_URL` | `http://presidio-analyzer:5001` | Text detection service. |
| `PRESIDIO_IMAGE_REDACTOR_URL` | `http://presidio-image-redactor:5003` | Image detection service. |
| `PRESIDIO_TIMEOUT_SECONDS` | `10` | Per-request timeout. |
| `PRESIDIO_SCORE_THRESHOLD` | `0.7` | Minimum detection confidence, `[0,1]`. |
| `PRESIDIO_ENTITIES` | `PERSON,ORGANIZATION` | Entity types to detect. `LOCATION` excluded by default. |
| `PRESIDIO_LANGUAGE` | `en` | Language forwarded to Presidio. |

## Redis

| Variable | Default | Notes |
| --- | --- | --- |
| `REDIS_URL` | `redis://redis:6379/0` | Connection URL. |
| `REDIS_SESSION_TTL_SECONDS` | `3600` | Mapping lifetime, refreshed on access. |
| `REDIS_KEY_PREFIX` | `palena:pseudonymizer` | Key namespace. |
| `REDIS_TIMEOUT_SECONDS` | `2` | Per-command timeout. |
| `REDIS_POOL_SIZE` | `10` | Connection pool size. |

## Text & session

| Variable | Default | Notes |
| --- | --- | --- |
| `SESSION_METADATA_KEY` | `session_id` | Metadata field read for the session id. |
| `DECOMPOSE_PERSON_NAMES` | `true` | Register first/last-name sub-mappings. |
| `POOL_PERSON` | *(unisex names)* | Comma-separated person pseudonyms. |
| `POOL_ORGANIZATION` | *(neutral orgs)* | Comma-separated org pseudonyms. |
| `POOL_LOCATION` | *(neutral places)* | Used only if `LOCATION` is enabled. |

## Non-text content (images)

| Variable | Default | Notes |
| --- | --- | --- |
| `NON_TEXT_PII_ACTION` | `redact` | `redact` \| `block` \| `passthrough`. |
| `MAX_IMAGE_BYTES` | `20971520` | Per-image cap (20 MiB). |
| `MAX_IMAGES_PER_REQUEST` | `20` | Request-level image limit. |

::: tip Deploying with Helm?
The chart maps these to friendly `values.yaml` keys — you rarely set the raw
env vars by hand. See [Deploy with Helm](/guide/deployment).
:::
