# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.
"""NER service for the pseudonymizer: a GLiNER2 span model over HTTP.

    POST /v1/detect  {"texts": [...], "labels": [...], "threshold": 0.9}
                  -> {"results": [[{"label", "start", "end", "score"}, ...], ...]}
    GET  /health     200 once the model is loaded, 503 before

Offsets are character offsets (Unicode code points) into each text. The model
needs no language hint: one multilingual model serves every language.

The pseudonymizer sends one message (or one window of a long message) per
text; inputs past NER_MAX_TEXT_CHARS are refused with 413, because the model
has no input cap of its own — its attention memory grows with the square of
the length (a 48k-character text took more than 23 GB) and past ~512 tokens it
stops finding names anyway.

Request text is personal data: it is never logged, only counts and timings.
"""

from __future__ import annotations

import logging
import os
import threading
import time
from collections.abc import Callable
from contextlib import asynccontextmanager
from typing import Any, Protocol

from fastapi import FastAPI, HTTPException, Response
from pydantic import BaseModel, Field

log = logging.getLogger("ner")


def _env_int(name: str, default: int) -> int:
    raw = os.environ.get(name, "")
    return int(raw) if raw.strip() else default


MODEL_DIR = os.environ.get("NER_MODEL_DIR", "/models/gliner2-pii")
MAX_TEXTS = _env_int("NER_MAX_TEXTS", 64)
MAX_TEXT_CHARS = _env_int("NER_MAX_TEXT_CHARS", 4000)
MAX_LABELS = 32
BATCH_SIZE = _env_int("NER_BATCH_SIZE", 8)
# CPU threads torch may use; defaults to 2 because torch otherwise sizes its
# pool by the node's cores, not the container's CPU limit.
TORCH_THREADS = _env_int("NER_TORCH_THREADS", 2)
# Concurrent inferences; more than one only oversubscribes the CPU threads.
MAX_CONCURRENCY = _env_int("NER_MAX_CONCURRENCY", 1)


class Extractor(Protocol):
    def batch_extract_entities(self, texts: list[str], entity_types: list[str], **kwargs: Any) -> list[dict]: ...


class DetectRequest(BaseModel):
    texts: list[str] = Field(max_length=MAX_TEXTS)
    labels: list[str] = Field(min_length=1, max_length=MAX_LABELS)
    threshold: float = Field(ge=0.0, le=1.0)


class Span(BaseModel):
    label: str
    start: int
    end: int
    score: float


class DetectResponse(BaseModel):
    results: list[list[Span]]


def load_gliner() -> Extractor:
    """Load the model baked into the image (no network: HF_HUB_OFFLINE=1)."""
    import torch
    from gliner2 import AutoExtractor

    torch.set_num_threads(TORCH_THREADS)
    model = AutoExtractor.from_pretrained(MODEL_DIR)
    model.eval()
    return model


def spans_of(result: dict, text: str, labels: set[str]) -> list[Span]:
    """Flatten one gliner2 result ({"entities": {label: [{start, end, confidence}]}})."""
    out: list[Span] = []
    for label, hits in (result.get("entities") or {}).items():
        if label not in labels:
            continue
        for h in hits:
            start, end = int(h["start"]), int(h["end"])
            if not 0 <= start < end <= len(text):
                raise ValueError(f"model returned span {start}-{end} outside a {len(text)}-char text")
            out.append(Span(label=label, start=start, end=end, score=round(float(h["confidence"]), 4)))
    out.sort(key=lambda s: (s.start, s.end, s.label))
    return out


def create_app(loader: Callable[[], Extractor] = load_gliner) -> FastAPI:
    state: dict[str, Any] = {"model": None}
    gate = threading.Semaphore(MAX_CONCURRENCY)

    @asynccontextmanager
    async def lifespan(_: FastAPI):
        t0 = time.perf_counter()
        state["model"] = loader()
        log.info("model loaded from %s in %.1fs", MODEL_DIR, time.perf_counter() - t0)
        yield

    app = FastAPI(title="palena-ner", lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

    @app.get("/health")
    def health(response: Response) -> dict:
        if state["model"] is None:
            response.status_code = 503
            return {"status": "loading"}
        return {"status": "ok"}

    @app.post("/v1/detect", response_model=DetectResponse)
    def detect(req: DetectRequest) -> DetectResponse:
        model = state["model"]
        if model is None:
            raise HTTPException(status_code=503, detail="model not loaded")
        too_long = [i for i, t in enumerate(req.texts) if len(t) > MAX_TEXT_CHARS]
        if too_long:
            raise HTTPException(
                status_code=413,
                detail=f"text {too_long[0]} is longer than {MAX_TEXT_CHARS} characters; send it in windows",
            )
        labels = list(dict.fromkeys(req.labels))
        # Blank texts never reach the model.
        idx = [i for i, t in enumerate(req.texts) if t.strip()]
        results: list[list[Span]] = [[] for _ in req.texts]
        if not idx:
            return DetectResponse(results=results)

        t0 = time.perf_counter()
        with gate:
            raw = model.batch_extract_entities(
                [req.texts[i] for i in idx],
                labels,
                batch_size=BATCH_SIZE,
                threshold=req.threshold,
                include_confidence=True,
                include_spans=True,
            )
        if len(raw) != len(idx):
            raise HTTPException(status_code=500, detail="model returned a result count that does not match")
        wanted = set(labels)
        try:
            for i, r in zip(idx, raw):
                results[i] = spans_of(r, req.texts[i], wanted)
        except ValueError as e:
            log.error("bad model output: %s", e)
            raise HTTPException(status_code=500, detail="model returned an invalid span") from None
        log.info(
            "detect texts=%d chars=%d spans=%d ms=%.0f",
            len(idx),
            sum(len(req.texts[i]) for i in idx),
            sum(len(r) for r in results),
            (time.perf_counter() - t0) * 1000,
        )
        return DetectResponse(results=results)

    return app


logging.basicConfig(level=os.environ.get("NER_LOG_LEVEL", "INFO").upper(), format="%(asctime)s %(levelname)s %(name)s %(message)s")
app = create_app() if os.environ.get("NER_SKIP_APP") != "1" else None
