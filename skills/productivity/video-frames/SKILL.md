---
name: video-frames
description: "Extract frames or short clips from videos using ffmpeg."
version: 1.0.0
author: Covo Agent
license: MIT
platforms: [linux, macos, windows]
prerequisites:
  commands: [ffmpeg]
metadata:
  tags: [video, ffmpeg, frame, extract, clip, media]
  related_skills: [canvas]
---

# Video Frames — Frame & Clip Extraction

Extract frames, thumbnails, and short clips from videos using ffmpeg.

## Single Frame

```bash
# At specific timestamp
ffmpeg -ss 00:01:30 -i video.mp4 -vframes 1 -q:v 2 frame.png

# At 10 seconds
ffmpeg -ss 10 -i video.mp4 -vframes 1 thumbnail.jpg

# Every N seconds (one frame per second)
ffmpeg -i video.mp4 -vf "fps=1" frames/frame_%04d.png
```

## Thumbnail Grid

```bash
# 3×3 thumbnail grid at intervals
ffmpeg -i video.mp4 -vf "
  fps=1/$(ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 video.mp4 | awk '{print int($1/9)}'),
  scale=320:240,
  tile=3x3
" grid.jpg
```

## Clip Extraction

```bash
# Cut 30-second clip
ffmpeg -ss 00:01:00 -t 30 -i video.mp4 -c copy clip.mp4

# Cut from start to 1 minute
ffmpeg -ss 0 -to 00:01:00 -i video.mp4 -c copy intro.mp4

# Extract without re-encoding (fast)
ffmpeg -ss 00:00:30 -to 00:01:00 -i video.mp4 -c copy -avoid_negative_ts make_zero clip.mp4
```

## GIF from Video

```bash
# 5-second GIF
ffmpeg -ss 00:00:10 -t 5 -i video.mp4 \
  -vf "fps=10,scale=320:-1:flags=lanczos" \
  -loop 0 output.gif
```

## Batch Processing

```bash
# Frame from every video in folder
for v in *.mp4; do
  ffmpeg -ss 5 -i "$v" -vframes 1 "thumbnails/${v%.mp4}.jpg"
done
```

## Info & Probe

```bash
# Duration
ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 video.mp4

# Resolution
ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=s=x:p=0 video.mp4

# Full info
ffprobe -v quiet -print_format json -show_format -show_streams video.mp4
```
