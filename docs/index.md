---
layout: home

hero:
  name: Palena Pseudonymizer
  text: Keep real names away from the LLM
  tagline: >-
    An HTTP guardrail for the LiteLLM proxy. It swaps real people and company
    names for consistent fictional ones before the model sees them, then
    restores the originals in the response — streaming included.
  image:
    src: /palena-icon.png
    alt: Palena
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: How it works
      link: /guide/how-it-works

features:
  - title: Bidirectional & consistent
    details: >-
      "Alice Johnson" becomes "Jordan Avery" on the way in and back to "Alice
      Johnson" on the way out — stable across every turn of a conversation.
  - title: Text, streaming & images
    details: >-
      Pseudonymizes chat text, reverses streamed responses without leaking
      partial names, and redacts PII in image attachments.
  - title: Fail-closed by design
    details: >-
      If detection or the mapping store is unreachable on the way to the model,
      the request is blocked — never silently leaked.
  - title: Deploy in one command
    details: >-
      A Helm chart ships the service plus optional Redis and Presidio, with
      organization detection on by default.
---
