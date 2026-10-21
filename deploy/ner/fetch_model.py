# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.
"""Download the pinned model revision into a directory (image build step).

    python fetch_model.py /models/gliner2-pii

The revision is a commit of the Hugging Face repo, so a rebuild gets the same
weights; bump it deliberately, after measuring the new revision on a labelled
multilingual test set.
"""

import sys

from huggingface_hub import snapshot_download

REPO = "fastino/gliner2-privacy-filter-PII-multi"
REVISION = "1cb4166094dc58fa8d836429f060d6c95f62b495"


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit("usage: fetch_model.py <target-dir>")
    path = snapshot_download(repo_id=REPO, revision=REVISION, local_dir=sys.argv[1])
    print(f"{REPO}@{REVISION} -> {path}")


if __name__ == "__main__":
    main()
