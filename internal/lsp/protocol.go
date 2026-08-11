package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	ErrorContentModified  = -32801
	ErrorRequestCancelled = -32800
	ErrorMethodNotFound   = -32601
)

type LSPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *LSPError) Error() string {
	return fmt.Sprintf("LSP error %d: %s", e.Code, e.Message)
}

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *LSPError `json:"error,omitempty"`
}

type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func encodeMessage(obj any) ([]byte, error) {
	body, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode message: %w", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	return append([]byte(header), body...), nil
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	var contentLength int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read header: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if contentLength == 0 {
				continue
			}
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if strings.EqualFold(key, "Content-Length") {
			contentLength, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
	}

	if contentLength <= 0 || contentLength > 100*1024*1024 {
		return nil, fmt.Errorf("invalid content-length: %d", contentLength)
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(r, body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func classifyMessage(data []byte) (string, int, string, error) {
	var raw struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", 0, "", fmt.Errorf("classify message: %w", err)
	}
	if raw.Method != "" && raw.ID > 0 {
		return "request", raw.ID, raw.Method, nil
	}
	if raw.Method != "" && raw.ID == 0 {
		return "notification", 0, raw.Method, nil
	}
	if raw.Error != nil {
		return "error", raw.ID, raw.Error.Message, nil
	}
	return "response", raw.ID, "", nil
}
