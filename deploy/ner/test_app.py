# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.
"""Tests for the NER service with a stand-in model (no torch needed).

    pip install -r deploy/ner/requirements-test.txt
    NER_SKIP_APP=1 python -m pytest deploy/ner
"""

import os

os.environ["NER_SKIP_APP"] = "1"

import pytest  # noqa: E402
from fastapi.testclient import TestClient  # noqa: E402

import app as ner_app  # noqa: E402


class FakeModel:
    """Finds the names it knows, like gliner2's batch_extract_entities."""

    def __init__(self, names):
        self.names = names
        self.calls = []

    def batch_extract_entities(self, texts, entity_types, **kwargs):
        self.calls.append((list(texts), list(entity_types), kwargs))
        out = []
        for t in texts:
            ents = {}
            for name, label in self.names.items():
                i = t.find(name)
                if i >= 0 and label in entity_types:
                    ents.setdefault(label, []).append({"text": name, "start": i, "end": i + len(name), "confidence": 0.97})
            out.append({"entities": ents})
        return out


@pytest.fixture
def model():
    return FakeModel({"Μαρία Παπαδοπούλου": "person", "Kovács Péter": "full_name"})


@pytest.fixture
def client(model):
    with TestClient(ner_app.create_app(loader=lambda: model)) as c:
        yield c


def test_health_ok_after_load(client):
    r = client.get("/health")
    assert r.status_code == 200
    assert r.json() == {"status": "ok"}


def test_detect_returns_character_offsets_parallel_to_texts(client, model):
    texts = ["Ενημέρωσε τη Μαρία Παπαδοπούλου.", "   ", "Küldd el Kovács Péternek."]
    r = client.post("/v1/detect", json={"texts": texts, "labels": ["person", "full_name"], "threshold": 0.9})
    assert r.status_code == 200
    res = r.json()["results"]
    assert len(res) == 3 and res[1] == []
    s = res[0][0]
    assert texts[0][s["start"] : s["end"]] == "Μαρία Παπαδοπούλου"
    assert res[2][0]["label"] == "full_name"
    # The blank text never reached the model; threshold and spans were asked.
    sent, labels, kwargs = model.calls[0]
    assert sent == [texts[0], texts[2]]
    assert kwargs["threshold"] == 0.9 and kwargs["include_spans"] and kwargs["include_confidence"]


def test_detect_drops_labels_not_asked(client):
    r = client.post("/v1/detect", json={"texts": ["Küldd el Kovács Péternek."], "labels": ["person"], "threshold": 0.5})
    assert r.json()["results"] == [[]]


def test_detect_refuses_overlong_text(client):
    r = client.post("/v1/detect", json={"texts": ["x" * (ner_app.MAX_TEXT_CHARS + 1)], "labels": ["person"], "threshold": 0.5})
    assert r.status_code == 413


def test_detect_validates_request(client):
    assert client.post("/v1/detect", json={"texts": ["a"], "labels": [], "threshold": 0.5}).status_code == 422
    assert client.post("/v1/detect", json={"texts": ["a"], "labels": ["person"], "threshold": 1.5}).status_code == 422
    too_many = ["a"] * (ner_app.MAX_TEXTS + 1)
    assert client.post("/v1/detect", json={"texts": too_many, "labels": ["person"], "threshold": 0.5}).status_code == 422


def test_span_outside_text_is_an_error():
    class Broken(FakeModel):
        def batch_extract_entities(self, texts, entity_types, **kwargs):
            return [{"entities": {"person": [{"start": 0, "end": 999, "confidence": 1.0}]}} for _ in texts]

    with TestClient(ner_app.create_app(loader=lambda: Broken({}))) as c:
        r = c.post("/v1/detect", json={"texts": ["abc"], "labels": ["person"], "threshold": 0.5})
        assert r.status_code == 500
