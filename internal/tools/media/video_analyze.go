package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/covoyage/covonaut/agentcore"
)

const (
	videoAnalyzeMaxBytes        = 50 * 1024 * 1024
	videoAnalyzeDefaultFrames   = 6
	videoAnalyzeMinFrames       = 1
	videoAnalyzeMaxFrames       = 8
	videoAnalyzeFrameMaxChars   = 400
	videoAnalyzeTotalMaxChars   = 2400
	videoAnalyzeDownloadTimeout = 60 * time.Second
)

// lookPath is exec.LookPath; tests may stub it.
var lookPath = exec.LookPath

// extractVideoFrames is the ffmpeg-backed sampler; tests may stub it.
var extractVideoFrames = defaultExtractVideoFrames

type videoFrame struct {
	Index       int     `json:"index"`
	Seconds     float64 `json:"seconds"`
	Description string  `json:"description"`
	Error       string  `json:"error,omitempty"`
}

func BuildVideoAnalyzeTool(workDir string, analyze ImageAnalyzer) *agentcore.Tool {
	return BuildVideoAnalyzeToolWithTransport(workDir, analyze, nil)
}

// BuildVideoAnalyzeToolWithTransport samples a video into stills and describes
// them with the vision model. Remote http(s) URLs need a safe transport.
func BuildVideoAnalyzeToolWithTransport(workDir string, analyze ImageAnalyzer, transport http.RoundTripper) *agentcore.Tool {
	return &agentcore.Tool{
		Name: "video_analyze",
		Description: strings.Join([]string{
			"Analyze visual content of a video by sampling still frames with ffmpeg. Prefer the unified read tool.",
			"Pass a workspace-relative or absolute local path, or an http(s) URL.",
			"Remote fetches require a safe transport (SSRF-checked) and are capped at 50MB.",
			"This is visual-only (scenes, on-screen text, UI, motion cues). Use transcribe for speech.",
			"Requires ffmpeg. Default 6 frames (max 8). Optional start_seconds/end_seconds crop the sample window.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Local video path or http(s) URL.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "Optional question or focus for the vision model.",
				},
				"frames": map[string]any{
					"type":        "integer",
					"description": "Number of stills to sample (1-8, default 6).",
				},
				"start_seconds": map[string]any{
					"type":        "number",
					"description": "Optional start of the sample window in seconds.",
				},
				"end_seconds": map[string]any{
					"type":        "number",
					"description": "Optional end of the sample window in seconds.",
				},
			},
			"required": []string{"path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Path         string   `json:"path"`
				Prompt       string   `json:"prompt"`
				Frames       int      `json:"frames"`
				StartSeconds *float64 `json:"start_seconds"`
				EndSeconds   *float64 `json:"end_seconds"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			return AnalyzeVideo(ctx, AnalyzeVideoInput{
				Path:         strings.TrimSpace(params.Path),
				Prompt:       params.Prompt,
				Frames:       params.Frames,
				StartSeconds: params.StartSeconds,
				EndSeconds:   params.EndSeconds,
				WorkDir:      workDir,
				Analyze:      analyze,
				Transport:    transport,
			})
		},
	}
}

// AnalyzeVideoInput is the programmatic entry for sampling a video into stills.
type AnalyzeVideoInput struct {
	Path         string
	Prompt       string
	Frames       int
	StartSeconds *float64
	EndSeconds   *float64
	WorkDir      string
	Analyze      ImageAnalyzer
	Transport    http.RoundTripper
}

// AnalyzeVideo samples a video into stills and describes them with the vision model.
func AnalyzeVideo(ctx context.Context, in AnalyzeVideoInput) (any, error) {
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	n := in.Frames
	if n == 0 {
		n = videoAnalyzeDefaultFrames
	}
	if n < videoAnalyzeMinFrames || n > videoAnalyzeMaxFrames {
		return nil, fmt.Errorf("frames must be between %d and %d", videoAnalyzeMinFrames, videoAnalyzeMaxFrames)
	}
	if _, err := lookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg is required for video_analyze")
	}
	if in.Analyze == nil {
		return nil, fmt.Errorf("vision model is not configured")
	}

	localPath, source, size, cleanup, err := materializeVideo(ctx, path, in.WorkDir, in.Transport)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	start, end := 0.0, 0.0
	if in.StartSeconds != nil {
		start = *in.StartSeconds
	}
	if in.EndSeconds != nil {
		end = *in.EndSeconds
	}
	if start < 0 {
		return nil, fmt.Errorf("start_seconds must be >= 0")
	}
	if end < 0 {
		return nil, fmt.Errorf("end_seconds must be >= 0")
	}
	if end > 0 && end <= start {
		return nil, fmt.Errorf("end_seconds must be greater than start_seconds")
	}

	stills, err := extractVideoFrames(ctx, localPath, n, start, end)
	if err != nil {
		return nil, err
	}
	if len(stills) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frames")
	}

	prompt := strings.TrimSpace(in.Prompt)
	framePrompt := videoFramePrompt(prompt)

	out := make([]videoFrame, 0, len(stills))
	var combined strings.Builder
	anyOK := false
	for i, still := range stills {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		item := videoFrame{Index: i + 1, Seconds: still.seconds}
		if len(still.png) == 0 {
			item.Error = "empty frame"
			out = append(out, item)
			continue
		}
		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(still.png)
		desc, aerr := in.Analyze(ctx, framePrompt, "image/png", dataURL)
		if aerr != nil {
			item.Error = aerr.Error()
			out = append(out, item)
			continue
		}
		desc, _ = truncateRunes(desc, videoAnalyzeFrameMaxChars)
		item.Description = desc
		anyOK = true
		if combined.Len() > 0 {
			combined.WriteByte('\n')
		}
		fmt.Fprintf(&combined, "Frame %d @ %.2fs: %s", item.Index, item.Seconds, desc)
		out = append(out, item)
	}
	if !anyOK {
		return nil, fmt.Errorf("vision model failed on all %d frames", len(stills))
	}
	summary, truncated := truncateRunes(combined.String(), videoAnalyzeTotalMaxChars)
	if prompt == "" {
		prompt = defaultVideoPrompt
	}
	return map[string]any{
		"path":        source,
		"bytes":       size,
		"frames":      n,
		"sampled":     out,
		"prompt":      prompt,
		"description": summary,
		"truncated":   truncated,
	}, nil
}

const defaultVideoPrompt = "Describe what is happening visually in this video. Note scene changes, on-screen text, UI, people, objects, and notable motion. Do not transcribe speech; use transcribe for audio."

func videoFramePrompt(userPrompt string) string {
	base := "Describe this video frame. Call out visible text, UI, people, objects, and motion cues."
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return base
	}
	return base + " User question: " + userPrompt
}

func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s, false
	}
	r := []rune(s)
	return string(r[:max]) + "…", true
}

type extractedStill struct {
	seconds float64
	png     []byte
}

func materializeVideo(ctx context.Context, path, workDir string, transport http.RoundTripper) (localPath, source string, size int64, cleanup func(), err error) {
	if u, perr := url.Parse(path); perr == nil && (u.Scheme == "http" || u.Scheme == "https") {
		localPath, size, cleanup, err = fetchRemoteVideo(ctx, u, transport)
		return localPath, u.String(), size, cleanup, err
	}
	if strings.HasPrefix(strings.ToLower(path), "file://") {
		if u, perr := url.Parse(path); perr == nil {
			path = u.Path
		}
	}
	abs, err := resolveLocalVideo(path, workDir)
	if err != nil {
		return "", "", 0, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", 0, nil, fmt.Errorf("stat video: %w", err)
	}
	if info.IsDir() {
		return "", "", 0, nil, fmt.Errorf("path is a directory")
	}
	return abs, abs, info.Size(), nil, nil
}

func resolveLocalVideo(path, workDir string) (string, error) {
	if strings.Contains(path, "://") {
		if u, perr := url.Parse(path); perr == nil && u.Scheme != "" && u.Scheme != "file" {
			return "", fmt.Errorf("remote URLs are not allowed; pass a local file path")
		}
	}
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	if workDir != "" {
		if err := allowMediaPath(abs, workDir, "video"); err != nil {
			return "", err
		}
	}
	if mt := videoExtOK(abs); mt == "" {
		return "", fmt.Errorf("unsupported video type (use mp4, webm, mov, avi, mkv, mpeg, mpg, m4v)")
	}
	return abs, nil
}

func fetchRemoteVideo(ctx context.Context, u *url.URL, transport http.RoundTripper) (localPath string, size int64, cleanup func(), err error) {
	if transport == nil {
		return "", 0, nil, fmt.Errorf("remote URLs require a safe HTTP transport; pass a local file path")
	}
	if u.Host == "" {
		return "", 0, nil, fmt.Errorf("invalid video url")
	}
	client := &http.Client{Timeout: videoAnalyzeDownloadTimeout, Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, nil, fmt.Errorf("fetch video: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, nil, fmt.Errorf("fetch video: HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, videoAnalyzeMaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", 0, nil, fmt.Errorf("read video: %w", err)
	}
	if len(data) > videoAnalyzeMaxBytes {
		return "", 0, nil, fmt.Errorf("video too large (%d bytes, max %d)", len(data), videoAnalyzeMaxBytes)
	}
	if len(data) == 0 {
		return "", 0, nil, fmt.Errorf("empty video response")
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	if videoExtOK(u.Path) == "" {
		ext = ".mp4"
	}
	dir, err := os.MkdirTemp("", "covo-video-*")
	if err != nil {
		return "", 0, nil, fmt.Errorf("temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	localPath = filepath.Join(dir, "input"+ext)
	if err := os.WriteFile(localPath, data, 0600); err != nil {
		cleanup()
		return "", 0, nil, fmt.Errorf("write video: %w", err)
	}
	return localPath, int64(len(data)), cleanup, nil
}

func defaultExtractVideoFrames(ctx context.Context, videoPath string, n int, start, end float64) ([]extractedStill, error) {
	dur, _ := probeVideoDuration(ctx, videoPath)
	windowStart, windowEnd := sampleWindow(dur, start, end)
	stamps := sampleTimestamps(windowStart, windowEnd, n)
	out := make([]extractedStill, 0, len(stamps))
	for _, ts := range stamps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		png, err := ffmpegPNGAt(ctx, videoPath, ts)
		if err != nil {
			return nil, fmt.Errorf("extract frame at %.2fs: %w", ts, err)
		}
		out = append(out, extractedStill{seconds: ts, png: png})
	}
	return out, nil
}

func sampleWindow(duration, start, end float64) (float64, float64) {
	if start < 0 {
		start = 0
	}
	if duration > 0 {
		if start >= duration {
			start = 0
		}
		if end <= 0 || end > duration {
			end = duration
		}
	} else if end <= 0 {
		end = start + float64(videoAnalyzeDefaultFrames*2)
		if end <= start {
			end = start + 1
		}
	}
	if end <= start {
		end = start + 1
	}
	return start, end
}

func sampleTimestamps(start, end float64, n int) []float64 {
	if n < 1 {
		n = 1
	}
	span := end - start
	if span <= 0 {
		return []float64{start}
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = start + (float64(i)+0.5)*span/float64(n)
	}
	return out
}

func probeVideoDuration(ctx context.Context, videoPath string) (float64, error) {
	if bin, err := lookPath("ffprobe"); err == nil {
		cmd := exec.CommandContext(ctx, bin,
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			videoPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			s := strings.TrimSpace(stdout.String())
			if d, perr := strconv.ParseFloat(s, 64); perr == nil && d > 0 {
				return d, nil
			}
		}
	}
	bin, err := lookPath("ffmpeg")
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-i", videoPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if d, ok := parseFFmpegDuration(stderr.String()); ok {
		return d, nil
	}
	return 0, fmt.Errorf("could not probe duration")
}

var ffmpegDurationRe = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

func parseFFmpegDuration(stderr string) (float64, bool) {
	m := ffmpegDurationRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return 0, false
	}
	d := float64(h*3600+min*60) + sec
	if d <= 0 {
		return 0, false
	}
	return d, true
}

func ffmpegPNGAt(ctx context.Context, videoPath string, seconds float64) ([]byte, error) {
	bin, err := lookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg is required for video_analyze")
	}
	cmd := exec.CommandContext(ctx, bin,
		"-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(seconds, 'f', 3, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	data := stdout.Bytes()
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, fmt.Errorf("ffmpeg did not return a png frame")
	}
	if len(data) > viewImageMaxBytes {
		return nil, fmt.Errorf("frame too large (%d bytes, max %d)", len(data), viewImageMaxBytes)
	}
	return data, nil
}

func videoExtOK(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	case ".ogv":
		return "video/ogg"
	default:
		return ""
	}
}
