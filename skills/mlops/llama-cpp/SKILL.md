---
name: llama-cpp
description: "Local LLM inference with llama.cpp: download GGUF models from HuggingFace Hub and run inference."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [llm, local, inference, gguf, llama, huggingface, quantize]
  related_skills: [huggingface-hub]
---

# llama.cpp — Local LLM Inference

Run large language models locally using llama.cpp's high-performance GGUF
format. Use for offline inference, privacy-sensitive tasks, or quick
prototyping without API costs.

## When to Use

- "run a local model"
- "try Mistral locally"
- "quantize this model"  
- "find a GGUF model for..."
- "compare model outputs locally"

## Installation

```bash
# macOS
brew install llama.cpp

# pip
pip install llama-cpp-python

# Or build from source
git clone https://github.com/ggerganov/llama.cpp
cd llama.cpp && make
```

## Find Models on HuggingFace Hub

```bash
# Search for GGUF models
python3 << 'EOF'
from huggingface_hub import list_models
models = list_models(library="gguf", sort="downloads", direction=-1, limit=20)
for m in models:
    print(f"{m.modelId} — {m.downloads:,} downloads")
EOF

# Common GGUF repos:
# TheBloke/Mistral-7B-Instruct-v0.2-GGUF
# TheBloke/Llama-3-8B-Instruct-GGUF
# bartowski/Phi-3-mini-4k-instruct-GGUF
# MaziyarPanahi/Qwen2-7B-Instruct-GGUF
```

## Download a Model

```bash
# Via huggingface-cli
huggingface-cli download TheBloke/Mistral-7B-Instruct-v0.2-GGUF \
  mistral-7b-instruct-v0.2.Q4_K_M.gguf \
  --local-dir ./models
```

## Run Inference (Python)

```python
from llama_cpp import Llama

llm = Llama(
    model_path="./models/mistral-7b-instruct-v0.2.Q4_K_M.gguf",
    n_ctx=4096,        # context window
    n_threads=8,        # CPU threads
    n_gpu_layers=-1,    # offload all layers to GPU (Metal/CUDA)
)

output = llm(
    "Q: What is the capital of France?\nA:",
    max_tokens=128,
    temperature=0.7,
    stop=["Q:", "\n\n"],
)
print(output["choices"][0]["text"])
```

## Quantization Guide

| Quant | Size (7B) | Quality | Use Case |
|-------|-----------|---------|----------|
| Q2_K | 2.8 GB | Minimal | Edge/mobile |
| Q4_K_M | 4.4 GB | Good (recommended) | General use |
| Q5_K_M | 5.2 GB | Better | Accuracy-critical |
| Q6_K | 6.0 GB | Near-perfect | Almost lossless |
| Q8_0 | 7.7 GB | Perfect | Reference quality |

## Server Mode

```bash
# Start OpenAI-compatible API server
python3 -m llama_cpp.server \
  --model ./models/mistral-7b-instruct-v0.2.Q4_K_M.gguf \
  --host 0.0.0.0 --port 8000 \
  --n_ctx 4096 --n_gpu_layers -1
```
