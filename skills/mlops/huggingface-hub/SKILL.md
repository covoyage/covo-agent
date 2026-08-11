---
name: huggingface-hub
description: "HuggingFace Hub CLI: search, download, upload models and datasets, manage repos."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [huggingface, models, datasets, hub, download, upload]
  related_skills: [llama-cpp]
---

# HuggingFace Hub — Model & Dataset Management

Interact with the HuggingFace Hub: search for models/datasets, download them,
upload your own, and manage repositories.

## Installation

```bash
pip install huggingface-hub
# Login (for private repos / uploads)
huggingface-cli login
```

## Search

```bash
# Search models
huggingface-cli search mistral --library pytorch --limit 10

# Via Python
python3 << 'EOF'
from huggingface_hub import list_models
models = list_models(
    library="transformers",
    language="zh",
    sort="downloads",
    direction=-1,
    limit=20,
)
for m in models:
    print(f"{m.modelId} — {m.downloads:,} downloads — {', '.join(m.tags or [])}")
EOF

# Search datasets
from huggingface_hub import list_datasets
datasets = list_datasets(sort="downloads", limit=10)
```

## Download

```bash
# CLI
huggingface-cli download meta-llama/Meta-Llama-3-8B --local-dir ./models/llama3

# Python
from huggingface_hub import snapshot_download
snapshot_download(
    "meta-llama/Meta-Llama-3-8B",
    local_dir="./models/llama3",
    ignore_patterns=["*.bin", "*.safetensors"],  # skip large files
)

# Download specific files
from huggingface_hub import hf_hub_download
hf_hub_download("bert-base-uncased", "config.json")
```

## Upload

```bash
# Create repo
huggingface-cli repo create my-model --type model

# Upload files
huggingface-cli upload my-username/my-model ./local-dir

# Python
from huggingface_hub import HfApi
api = HfApi()
api.create_repo("my-model", private=True)
api.upload_folder(
    folder_path="./local-dir",
    repo_id="my-username/my-model",
)
```

## Key APIs

```python
from huggingface_hub import HfApi, HfFileSystem
api = HfApi()

# Repo info
api.model_info("bert-base-uncased")

# List files
api.list_repo_files("bert-base-uncased")

# Read file content
from huggingface_hub import hf_hub_download
config = hf_hub_download("bert-base-uncased", "config.json", 
    cache_dir="./cache")

# File system access (like a virtual filesystem)
fs = HfFileSystem()
fs.ls("datasets/squad", detail=False)
with fs.open("datasets/squad/README.md", "r") as f:
    print(f.read())
```

## Common Tasks

```bash
# Find trending models
huggingface-cli list-models --sort downloads --limit 20

# Download with resume support
huggingface-cli download model-id --resume-download

# Check disk usage
du -sh ~/.cache/huggingface/

# Clean cache
huggingface-cli delete-cache
```
