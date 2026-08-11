#!/usr/bin/env python3
"""Push-to-talk recording for covo-agent.

Usage: python3 voice_record.py <duration> [language]

Requires: pip install numpy sounddevice
"""
import sys
import os
import tempfile
import wave

try:
    import numpy as np
except ImportError:
    print("ERROR: numpy not installed. Run: pip install numpy", file=sys.stderr)
    sys.exit(1)

try:
    import sounddevice as sd
except ImportError:
    print("ERROR: sounddevice not installed. Run: pip install sounddevice",
          file=sys.stderr)
    sys.exit(1)

DURATION = int(sys.argv[1]) if len(sys.argv) > 1 else 15
SAMPLE_RATE = 16000
CHUNK = 1280

print(f"Recording for {DURATION} seconds... Speak now!", file=sys.stderr, flush=True)

frames = []
total_chunks = int(SAMPLE_RATE * DURATION / CHUNK)
for i in range(total_chunks):
    data, _ = sd.rec(CHUNK, samplerate=SAMPLE_RATE, channels=1, dtype='int16')
    sd.wait()
    frames.append(data.flatten())
    if (i + 1) % (total_chunks // 5 or 1) == 0:
        pct = int((i + 1) / total_chunks * 100)
        print(f"Recording... {pct}%", file=sys.stderr, flush=True)

audio = np.concatenate(frames)

rms = np.sqrt(np.mean(audio.astype(float) ** 2))
if rms < 100:
    print("SILENCE: No audible speech detected", flush=True)
    sys.exit(0)

tmpfile = os.path.join(tempfile.gettempdir(), f"covo-ptt-{int(time.time())}.wav")
with wave.open(tmpfile, 'wb') as wf:
    wf.setnchannels(1)
    wf.setsampwidth(2)
    wf.setframerate(SAMPLE_RATE)
    wf.writeframes(audio.tobytes())

print(f"RECORDED:{tmpfile}", flush=True)
