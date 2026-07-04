# Configuration

All settings are environment variables prefixed `PALENA_PSEUDONYMIZER_`. The
service validates them on startup and refuses to boot on invalid config. The
full list is in the [Environment variables](/reference/environment) reference;
this page covers the choices that matter most.

## Entities

```bash
PALENA_PSEUDONYMIZER_PRESIDIO_ENTITIES=PERSON,ORGANIZATION   # default
```

`LOCATION` is **off by default**. Masking a city (`Paris` → `Riverside`)
replaces one real place with another real-sounding place, and the model then
reasons from the wrong geography — wrong weather, wrong currency, "which
Riverside do you mean?". Person and organization names are pure identity and
safe to swap; place names carry semantics the model needs. Add `LOCATION` back
only if your threat model requires geography masking.

You can enable **any entity type Presidio detects** — add structured PII such
as credit cards, national IDs, emails or phone numbers:

```bash
PALENA_PSEUDONYMIZER_PRESIDIO_ENTITIES=PERSON,ORGANIZATION,CREDIT_CARD,US_SSN,EMAIL_ADDRESS
```

How each type is substituted depends on its [strategy](#substitution-strategy).

## Substitution strategy

Not all PII should be masked the same way. A fake *name* for a credit card is
meaningless, and the model rarely needs the real digits — it just needs to know
a value was there. So there are two strategies:

| Strategy | Output | Right for |
| --- | --- | --- |
| `pool` | a realistic fictional value (`Alice Johnson` → `Jordan Avery`) | **nominal identities** the model reasons about: `PERSON`, `ORGANIZATION`, `LOCATION` |
| `token` | a consistent reversible placeholder (`4111…` → `<CREDIT_CARD_1>`) | **structured PII**: `CREDIT_CARD`, `US_SSN`, `IBAN_CODE`, `EMAIL_ADDRESS`, phone numbers, insurance / ID numbers, custom types |

```bash
# Default strategy for enabled types that aren't nominal identities:
PALENA_PSEUDONYMIZER_ENTITY_STRATEGY_DEFAULT=token          # token | pool

# Per-type overrides (comma-separated TYPE:strategy):
PALENA_PSEUDONYMIZER_ENTITY_STRATEGY=PHONE_NUMBER:pool,CREDIT_CARD:token
```

**Resolution order** for a given entity type:

1. an explicit `ENTITY_STRATEGY` override, else
2. `pool` if the type is `PERSON` / `ORGANIZATION` / `LOCATION`, else
3. `ENTITY_STRATEGY_DEFAULT` (`token`).

So simply adding `CREDIT_CARD` to `PRESIDIO_ENTITIES` tokenizes it
automatically — no strategy config needed for the common case. Tokens are
`<TYPE_N>` where `N` is a per-type, per-session counter, so the same value maps
to the same token across a conversation and reverses back to the exact original
(case and all).

::: tip Detecting structured / country-specific PII
Presidio recognises many types out of the box (`CREDIT_CARD`, `US_SSN`,
`IBAN_CODE`, `EMAIL_ADDRESS`, `PHONE_NUMBER`, `US_PASSPORT`, `UK_NHS`, …).
Country-specific IDs (national identity cards, insurance numbers) usually need a
**custom recognizer** — add a regex or deny-list recognizer the same way you add
the [organization deny-list](/guide/organization-detection).
:::

## Pseudonym pools

Each entity type draws pseudonyms from a pool:

```bash
PALENA_PSEUDONYMIZER_POOL_PERSON=Jordan Avery,Taylor Morgan,Alex Rivera,...
PALENA_PSEUDONYMIZER_POOL_ORGANIZATION=Acme Corp,Birch Industries,...
```

The default PERSON pool is **gender-neutral** on purpose. A pseudonym carries
an implied gender the model reasons from — map a female name to "Thomas" and
the model writes "he", leaking a wrong attribute. Unisex names (Jordan, Taylor,
Alex, Riley…) give the model no strong prior, so it says "they" or asks. This
is pure pool curation; there is no gender logic in the service.

## Person-name decomposition

```bash
PALENA_PSEUDONYMIZER_DECOMPOSE_PERSON_NAMES=true   # default
```

When on, mapping "Alice Johnson → Jordan Avery" also records "Alice → Jordan"
and "Johnson → Avery", so a model that refers to just the first name still
reverses correctly and later turns stay consistent. A real surname that is also
a common noun (e.g. "Baker") can over-match; set this to `false` if your inputs
contain many such names.

## Images

```bash
PALENA_PSEUDONYMIZER_NON_TEXT_PII_ACTION=redact   # redact | block | passthrough
```

- **redact** (default) — forward a redacted copy of the image.
- **block** — reject the request with a user-facing message.
- **passthrough** — forward the original untouched (not recommended).

## Failure behaviour

The guardrail is **fail-closed on the way to the model**: if Presidio or Redis
is unreachable during the pre-call phase, the request is blocked so real names
cannot leak. On the post-call phase it is best-effort — a reversal failure
never turns a successful LLM response into an error.

Operators control the final policy with LiteLLM's `unreachable_fallback`
(`fail_closed` recommended).

## Authentication

```bash
PALENA_PSEUDONYMIZER_API_KEY=<shared-secret>
```

When set, clients (i.e. the LiteLLM proxy) must send it as an `x-api-key`
header. When empty, the service accepts any request — fine for a private
in-cluster deployment, not for an exposed one.
