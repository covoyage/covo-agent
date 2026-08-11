---
name: whisper
description: "Local speech-to-text with OpenAI Whisper: 99 languages, 6 model sizes."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [speech, transcription, whisper, audio, stt, local]
  related_skills: [tts, transcribe]
---

# Whisper — Local Speech Recognition

Transcribe audio locally with OpenAI Whisper. Supports 99 languages,
translation to English, and language identification.

## Installation

```bash
pip install openai-whisper
# GPU acceleration (optional):
pip install openai-whisper[gpu]
```

## Model Sizes

| Size | Params | Disk | Speed | Quality |
|------|--------|------|-------|---------|
| tiny | 39M | 75MB | 10x | Basic |
| base | 74M | 145MB | 7x | Good |
| small | 244M | 488MB | 4x | Better |
| medium | 769M | 1.5GB | 2x | Great |
| large-v2 | 1.5B | 3GB | 1x | Best |
| large-v3 | 1.5B | 3GB | 1x | Best (newest) |

## Usage

```python
import whisper

# Load model
model = whisper.load_model("base")

# Transcribe
result = model.transcribe("audio.mp3")
print(result["text"])

# With options
result = model.transcribe("audio.mp3",
    language="zh",        # force language
    task="translate",     # translate to English
    temperature=0.2,
    word_timestamps=True, # word-level timing
)

# Batch processing
import glob
for f in glob.glob("*.mp3"):
    result = model.transcribe(f)
    print(f"{f}: {result['text'][:100]}")
```

## CLI Usage

```bash
whisper audio.mp3 --model base --language en
whisper audio.mp3 --model medium --task translate
whisper meeting.wav --model large-v3 --word_timestamps True --output_format srt
```

## Language Codes

zh, en, ja, ko, fr, de, es, pt, ru, ar, hi, bn, vi, th, id, ms, tl, ...

Detect language: set `language=None` and check `result["language"]`.
