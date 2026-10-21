# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.
#
# NER service for the pseudonymizer (DETECTION_ENGINE=ner): GLiNER2-PII on
# CPU, with the pinned model revision baked in — the container needs no
# network access at runtime (HF_HUB_OFFLINE=1).
#
# Published as ghcr.io/palenaai/palena-ner:<release version>.
ARG PYTHON_VERSION=3.12

FROM python:${PYTHON_VERSION}-slim AS build
ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1
RUN python -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH
# CPU-only torch first, so gliner2 does not pull the CUDA build.
RUN pip install --index-url https://download.pytorch.org/whl/cpu torch==2.14.1
COPY requirements.txt /tmp/requirements.txt
RUN pip install -r /tmp/requirements.txt
COPY fetch_model.py /tmp/fetch_model.py
RUN python /tmp/fetch_model.py /models/gliner2-pii

FROM python:${PYTHON_VERSION}-slim
COPY --from=build /opt/venv /opt/venv
COPY --from=build /models /models
COPY app.py /app/app.py
ENV PATH=/opt/venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    HF_HOME=/tmp/hf \
    NER_MODEL_DIR=/models/gliner2-pii
WORKDIR /app
USER 65532:65532
EXPOSE 3000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "3000", "--workers", "1", "--no-server-header"]
