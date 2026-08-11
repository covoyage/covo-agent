#!/usr/bin/env python3
"""Wake word detection + voice recording for covo-agent.

Usage: python3 voice_listen.py <wake_word> <record_seconds> <max_duration>

Requires: pip install numpy sounddevice
Optional: pip install openwakeword (for wake word detection)
"""
import sys
import time
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
    print("ERROR: sounddevice not installed. Run: pip install sounddevice", file=sys.stderr)
    sys.exit(1)

# Try openwakeword for wake word detection
try:
    from openwakeword.model import Model as OwwModel
    use_openwakeword = True
except ImportError:
    use_openwakeword = False

WAKE_WORD = sys.argv[1] if len(sys.argv) > 1 else "hey covo"
RECORD_SEC = int(sys.argv[2]) if len(sys.argv) > 2 else 10
MAX_DURATION = int(sys.argv[3]) if len(sys.argv) > 3 else 300
SAMPLE_RATE = 16000
CHUNK = 1280


def record_audio(duration, filename):
    """Record audio for the specified duration."""
    frames = []
    total_chunks = int(SAMPLE_RATE * duration / CHUNK)
    for _ in range(total_chunks):
        data, _ = sd.rec(CHUNK, samplerate=SAMPLE_RATE, channels=1, dtype='int16')
        sd.wait()
        frames.append(data.flatten())
    audio = np.concatenate(frames)
    with wave.open(filename, 'wb') as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(SAMPLE_RATE)
        wf.writeframes(audio.tobytes())
    return filename


def listen_openwakeword():
    """Listen using openwakeword.

    openwakeword uses pre-trained models. Available built-in models include:
    "hey_jarvis", "alexa", "hey_mycroft", "timer", "weather".

    For custom wake words, train a model at https://github.com/dscripka/openwakeword
    and place the .onnx file in ~/.cache/openwakeword/.
    """
    model = None
    model_name = None
    for name in ["hey_jarvis", "alexa", "hey_mycroft"]:
        try:
            model = OwwModel(wakeword_models=[name])
            model_name = name
            break
        except Exception:
            continue

    if model is None:
        print("WARNING: No openwakeword model available, falling back to VAD",
              file=sys.stderr, flush=True)
        return listen_simple()

    print(f"Listening for wake word '{WAKE_WORD}' (model: {model_name})...", flush=True)
    start = time.time()
    while time.time() - start < MAX_DURATION:
        data, _ = sd.rec(CHUNK, samplerate=SAMPLE_RATE, channels=1, dtype='int16')
        sd.wait()
        prediction = model.predict(data.flatten())
        for name, score in prediction.items():
            if score > 0.5:
                print(f"WAKE_WORD_DETECTED (model={name}, score={score:.2f})", flush=True)
                tmpfile = os.path.join(tempfile.gettempdir(),
                                       f"covo-voice-{int(time.time())}.wav")
                record_audio(RECORD_SEC, tmpfile)
                print(f"RECORDED:{tmpfile}", flush=True)
                return tmpfile
    return None


def listen_simple():
    """Simple voice activity detection fallback."""
    print(f"Listening for voice activity (wake word: '{WAKE_WORD}')...", flush=True)
    print("NOTE: Install openwakeword for proper wake word detection:",
          file=sys.stderr)
    print("  pip install openwakeword", file=sys.stderr)
    start = time.time()
    while time.time() - start < MAX_DURATION:
        data, _ = sd.rec(CHUNK, samplerate=SAMPLE_RATE, channels=1, dtype='int16')
        sd.wait()
        rms = np.sqrt(np.mean(data.astype(float) ** 2))
        if rms > 500:
            print(f"VOICE_DETECTED (rms={rms:.0f})", flush=True)
            time.sleep(0.3)
            tmpfile = os.path.join(tempfile.gettempdir(),
                                   f"covo-voice-{int(time.time())}.wav")
            record_audio(RECORD_SEC, tmpfile)
            print(f"RECORDED:{tmpfile}", flush=True)
            return tmpfile
    return None


if use_openwakeword:
    result = listen_openwakeword()
else:
    result = listen_simple()

if result is None:
    print("TIMEOUT: No voice detected within duration", flush=True)
    sys.exit(0)
