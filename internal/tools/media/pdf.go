package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/covoyage/covonaut/agentcore"
)

func BuildPdfTool() *agentcore.Tool {
	return &agentcore.Tool{
		Name: "pdf",
		Description: strings.Join([]string{
			"Extract text content from PDF documents for analysis.",
			"Supports local files and page range selection.",
			"",
			"Uses pdftotext (poppler-utils) for extraction. Falls back to Python PyPDF2 if unavailable.",
			"Install: brew install poppler (macOS) or apt install poppler-utils (Linux).",
			"",
			"The extracted text is returned directly. For large PDFs, use the pages parameter",
			"to limit the extraction scope.",
		}, "\n"),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the PDF file.",
				},
				"pages": map[string]any{
					"type":        "string",
					"description": "Page range to extract (e.g. '1-5', '1,3,5-7'). Default: all pages.",
				},
				"max_pages": map[string]any{
					"type":        "integer",
					"description": "Maximum number of pages to extract (default: 50).",
				},
				"action": map[string]any{
					"type":        "string",
					"description": "Action: 'extract' (default) or 'info'.",
					"enum":        []string{"extract", "info"},
				},
			},
			"required": []string{"path"},
		},
		Func: func(ctx context.Context, args json.RawMessage) (any, error) {
			var params struct {
				Path     string `json:"path"`
				Pages    string `json:"pages"`
				MaxPages int    `json:"max_pages"`
				Action   string `json:"action"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}

			if params.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if params.MaxPages <= 0 {
				params.MaxPages = 50
			}
			if params.Action == "" {
				params.Action = "extract"
			}

			// Resolve path
			path := params.Path
			if strings.HasPrefix(path, "~") {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[1:])
			}

			if _, err := os.Stat(path); os.IsNotExist(err) {
				return nil, fmt.Errorf("file not found: %s", path)
			}

			switch params.Action {
			case "info":
				return pdfInfo(ctx, path)
			case "extract":
				return pdfExtract(ctx, path, params.Pages, params.MaxPages)
			default:
				return nil, fmt.Errorf("unknown action %q", params.Action)
			}
		},
	}
}

func pdfInfo(ctx context.Context, path string) (any, error) {
	// Try pdfinfo first
	if _, err := exec.LookPath("pdfinfo"); err == nil {
		cmd := exec.CommandContext(ctx, "pdfinfo", path)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err == nil {
			info := make(map[string]string)
			for _, line := range strings.Split(stdout.String(), "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
				}
			}
			return map[string]any{"status": "ok", "info": info}, nil
		}
	}

	// Fallback: use pdftotext to count pages
	count, err := pdfPageCount(ctx, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":     "ok",
		"page_count": count,
		"note":       "Install pdfinfo (poppler-utils) for detailed metadata",
	}, nil
}

func pdfExtract(ctx context.Context, path, pages string, maxPages int) (any, error) {
	// Try pdftotext first (poppler-utils)
	if _, err := exec.LookPath("pdftotext"); err == nil {
		return pdfExtractPdftotext(ctx, path, pages, maxPages)
	}

	// Fallback: Python PyPDF2
	return pdfExtractPython(ctx, path, pages, maxPages)
}

func pdfExtractPdftotext(ctx context.Context, path, pages string, maxPages int) (any, error) {
	var args []string

	if pages != "" {
		// pdftotext uses -f (first page) and -l (last page)
		first, last, err := parsePageRange(pages, maxPages)
		if err != nil {
			return nil, err
		}
		args = []string{"-layout", "-f", strconv.Itoa(first), "-l", strconv.Itoa(last), path, "-"}
	} else {
		args = []string{"-layout", "-l", strconv.Itoa(maxPages), path, "-"}
	}

	cmd := exec.CommandContext(ctx, "pdftotext", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext failed: %s", stderr.String())
	}

	text := stdout.String()
	if len(text) > 100000 {
		text = text[:100000] + "\n\n[Truncated at 100KB]"
	}

	wordCount := len(strings.Fields(text))
	return map[string]any{
		"status":     "extracted",
		"tool":       "pdftotext",
		"text":       text,
		"chars":      len(text),
		"word_count": wordCount,
	}, nil
}

func pdfExtractPython(ctx context.Context, path, pages string, maxPages int) (any, error) {
	script := fmt.Sprintf(`
import sys
try:
    from PyPDF2 import PdfReader
except ImportError:
    try:
        from pypdf import PdfReader
    except ImportError:
        print("ERROR: No PDF library found. Install: pip install pypdf", file=sys.stderr)
        sys.exit(1)

reader = PdfReader(%q)
total = len(reader.pages)
pages_str = %q
max_pages = %d

if pages_str:
    parts = pages_str.split(',')
    page_nums = []
    for p in parts:
        p = p.strip()
        if '-' in p:
            a, b = p.split('-', 1)
            for i in range(int(a), min(int(b), total) + 1):
                page_nums.append(i - 1)
        else:
            page_nums.append(int(p) - 1)
else:
    page_nums = list(range(min(total, max_pages)))

text_parts = []
for i in page_nums:
    if 0 <= i < total:
        page_text = reader.pages[i].extract_text() or ""
        if page_text.strip():
            text_parts.append(f"--- Page {i+1} ---\n{page_text}")

print(f"TOTAL_PAGES:{total}")
print("".join(text_parts))
`, path, pages, maxPages)

	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("PDF extraction failed. Install poppler-utils (brew install poppler) or pypdf (pip install pypdf): %s", stderr.String())
	}

	output := stdout.String()
	totalPages := 0
	text := output
	if idx := strings.Index(output, "TOTAL_PAGES:"); idx >= 0 {
		end := strings.Index(output[idx:], "\n")
		if end > 0 {
			totalPages, _ = strconv.Atoi(output[idx+12 : idx+end])
			text = output[idx+end+1:]
		}
	}

	if len(text) > 100000 {
		text = text[:100000] + "\n\n[Truncated at 100KB]"
	}

	return map[string]any{
		"status":      "extracted",
		"tool":        "pypdf",
		"text":        text,
		"total_pages": totalPages,
		"chars":       len(text),
	}, nil
}

func pdfPageCount(ctx context.Context, path string) (int, error) {
	if _, err := exec.LookPath("pdfinfo"); err == nil {
		cmd := exec.CommandContext(ctx, "pdfinfo", path)
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Run(); err == nil {
			for _, line := range strings.Split(stdout.String(), "\n") {
				if strings.HasPrefix(line, "Pages:") {
					count := strings.TrimSpace(strings.TrimPrefix(line, "Pages:"))
					n, _ := strconv.Atoi(count)
					return n, nil
				}
			}
		}
	}

	// Fallback: Python
	script := fmt.Sprintf(`
try:
    from PyPDF2 import PdfReader
except ImportError:
    from pypdf import PdfReader
print(len(PdfReader(%q).pages))
`, path)
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("cannot determine page count")
	}
	n, _ := strconv.Atoi(strings.TrimSpace(stdout.String()))
	return n, nil
}

// parsePageRange parses "1-5" or "1,3,5-7" into first and last page numbers.
func parsePageRange(pages string, maxPages int) (int, int, error) {
	pages = strings.TrimSpace(pages)
	if pages == "" {
		return 1, maxPages, nil
	}

	// Simple case: "N-M"
	if strings.Contains(pages, "-") && !strings.Contains(pages, ",") {
		parts := strings.SplitN(pages, "-", 2)
		first, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		last, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
		if first <= 0 {
			first = 1
		}
		if last <= 0 || last > maxPages {
			last = maxPages
		}
		return first, last, nil
	}

	// Complex case: find min and max
	minPage, maxPage := maxPages, 1
	for _, part := range strings.Split(pages, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			parts := strings.SplitN(part, "-", 2)
			a, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
			b, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			if a < minPage {
				minPage = a
			}
			if b > maxPage {
				maxPage = b
			}
		} else {
			n, _ := strconv.Atoi(part)
			if n < minPage {
				minPage = n
			}
			if n > maxPage {
				maxPage = n
			}
		}
	}
	if minPage <= 0 {
		minPage = 1
	}
	if maxPage > maxPages {
		maxPage = maxPages
	}
	return minPage, maxPage, nil
}
