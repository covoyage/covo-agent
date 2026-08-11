---
name: songsee
description: "Audio spectrograms and feature visualizations: mel, chroma, MFCC, waveforms."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [audio, spectrogram, visualization, mel, chroma, mfcc, analysis]
  related_skills: [video-frames, canvas]
---

# Songsee — Audio Visualization

Generate audio spectrograms and feature visualizations for analysis
or creative output.

## Installation

```bash
pip install librosa matplotlib numpy
```

## Spectrogram

```python
import librosa, librosa.display, matplotlib.pyplot as plt, numpy as np

y, sr = librosa.load("audio.mp3")

# Mel spectrogram
S = librosa.feature.melspectrogram(y=y, sr=sr, n_mels=128, fmax=8000)
S_db = librosa.power_to_db(S, ref=np.max)

plt.figure(figsize=(12, 4))
librosa.display.specshow(S_db, sr=sr, x_axis="time", y_axis="mel", cmap="magma")
plt.colorbar(format="%+2.0f dB")
plt.title("Mel Spectrogram")
plt.tight_layout()
plt.savefig("mel_spectrogram.png", dpi=150)
```

## Chromagram

```python
# Chroma (pitch class) features
chroma = librosa.feature.chroma_stft(y=y, sr=sr)

plt.figure(figsize=(12, 4))
librosa.display.specshow(chroma, sr=sr, x_axis="time", y_axis="chroma", cmap="coolwarm")
plt.colorbar()
plt.title("Chromagram")
plt.tight_layout()
plt.savefig("chromagram.png", dpi=150)
```

## MFCC (Voice/Instrument Characteristics)

```python
mfcc = librosa.feature.mfcc(y=y, sr=sr, n_mfcc=20)

plt.figure(figsize=(12, 4))
librosa.display.specshow(mfcc, sr=sr, x_axis="time", cmap="viridis")
plt.colorbar()
plt.title("MFCC")
plt.tight_layout()
plt.savefig("mfcc.png", dpi=150)
```

## Waveform

```python
plt.figure(figsize=(12, 3))
librosa.display.waveshow(y, sr=sr, alpha=0.6)
plt.title("Waveform")
plt.xlabel("Time")
plt.ylabel("Amplitude")
plt.tight_layout()
plt.savefig("waveform.png", dpi=150)
```

## Multi-Feature Panel

```python
fig, axes = plt.subplots(4, 1, figsize=(12, 10))

librosa.display.waveshow(y, sr=sr, ax=axes[0], alpha=0.6)
axes[0].set_title("Waveform")

librosa.display.specshow(S_db, sr=sr, x_axis="time", y_axis="mel", ax=axes[1], cmap="magma")
axes[1].set_title("Mel Spectrogram")

librosa.display.specshow(chroma, sr=sr, x_axis="time", y_axis="chroma", ax=axes[2], cmap="coolwarm")
axes[2].set_title("Chromagram")

librosa.display.specshow(mfcc, sr=sr, x_axis="time", ax=axes[3], cmap="viridis")
axes[3].set_title("MFCC")

plt.tight_layout()
plt.savefig("audio_features.png", dpi=150)
```

## Quick CLI

```bash
python3 << 'EOF'
import sys, librosa, librosa.display, matplotlib.pyplot as plt, numpy as np
audio = sys.argv[1] if len(sys.argv) > 1 else "audio.mp3"
y, sr = librosa.load(audio)
S = librosa.feature.melspectrogram(y=y, sr=sr)
S_db = librosa.power_to_db(S, ref=np.max)
plt.figure(figsize=(12, 4))
librosa.display.specshow(S_db, sr=sr, x_axis="time", y_axis="mel", cmap="magma")
plt.colorbar(format="%+2.0f dB")
plt.savefig("spectrogram.png", dpi=150, bbox_inches="tight")
print(f"Saved spectrogram.png ({len(y)/sr:.1f}s audio)")
EOF
```

## Show in Browser

Use `canvas` tool to display generated visualizations:
```json
{"mode": "html", "content": "<img src='spectrogram.png'>"}
```
