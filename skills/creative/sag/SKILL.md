---
name: sag
description: "ElevenLabs text-to-speech with macOS-style 'say' experience and voice customization."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [python3]
metadata:
  tags: [tts, speech, elevenlabs, voice, audio, say]
  related_skills: [tts]
---

# SAG — ElevenLabs Text-to-Speech

Natural-sounding text-to-speech via ElevenLabs API with voice cloning,
multilingual support, and streaming.

## Setup

```bash
pip install elevenlabs
# Set API key
export ELEVENLABS_API_KEY="your-key"
```

## Basic Usage

```bash
# Quick speak (like macOS 'say')
python3 -c "
from elevenlabs import play, Voice, VoiceSettings
voice = Voice(voice_id='21m00Tcm4TlvDq8ikWAM',  # Rachel
    settings=VoiceSettings(stability=0.5, similarity_boost=0.75))
play('Hello, this is a test of ElevenLabs text to speech.', voice=voice)
"

# Save to file
python3 -c "
from elevenlabs import save, Voice
save('This will be saved as an MP3 file.', Voice(voice_id='21m00Tcm4TlvDq8ikWAM'),
    filename='output.mp3')
"
```

## Voice Library

```python
from elevenlabs import voices

for voice in voices():
    print(f"{voice.name:20s} | {voice.voice_id} | {', '.join(voice.labels or [])}")
```

## Common Voices

| ID | Name | Style |
|----|------|-------|
| `21m00Tcm4TlvDq8ikWAM` | Rachel | Warm, natural female |
| `AZnzlk1XvdvUeBnXmlld` | Domi | Friendly, casual |
| `EXAVITQu4vr4xnSDxMaL` | Bella | Soft, gentle |
| `yoZ06aMxZJJ28mfd3POQ` | Sam | Deep, authoritative male |
| `MF3mGyEYCl7XYWbV9V6O` | Elli | Young, energetic |
| `VR6AewLTigWG4xSOukaG` | Arnold | Deep, commanding |
| `pNInz6obpgDQGcFmaJgB` | Adam | Professional male |

## Multilingual

```python
from elevenlabs import save, Voice

# Chinese
save("你好，这是ElevenLabs的语音合成测试。", 
    Voice(voice_id='21m00Tcm4TlvDq8ikWAM'),
    filename="chinese.mp3")

# Japanese
save("こんにちは、これはテストです。",
    Voice(voice_id='21m00Tcm4TlvDq8ikWAM'),
    filename="japanese.mp3")
```

## Streaming

```python
from elevenlabs import stream, Voice

audio_stream = stream("This is streaming in real-time.",
    Voice(voice_id='21m00Tcm4TlvDq8ikWAM'))
# Process stream...
```

## Voice Settings

```python
from elevenlabs import Voice, VoiceSettings

voice = Voice(
    voice_id='21m00Tcm4TlvDq8ikWAM',
    settings=VoiceSettings(
        stability=0.5,          # 0-1, higher = more monotone
        similarity_boost=0.75,  # 0-1, higher = closer to original voice
        style=0.0,             # 0-1, higher = more expressive
        use_speaker_boost=True, # Boost clarity
    )
)
```

## macOS `say` Wrapper

```bash
# If ElevenLabs is not configured, fall back to macOS say
if [ -n "$ELEVENLABS_API_KEY" ]; then
    python3 -c "from elevenlabs import play; play('$1')"
else
    say "$1"
fi
```
