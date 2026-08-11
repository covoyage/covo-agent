package agent

import (
	"encoding/json"
	"strings"
)

var fileMutatingToolNames = map[string]bool{
	"write_file": true,
	"patch":      true,
	"write":      true,
}

func FileMutationResultLanded(toolName string, result any) bool {
	if !fileMutatingToolNames[toolName] {
		return false
	}

	resultStr, ok := result.(string)
	if !ok {
		return false
	}

	data := make(map[string]any)
	if err := json.Unmarshal([]byte(strings.TrimSpace(resultStr)), &data); err != nil {
		return false
	}

	if errVal, ok := data["error"]; ok && errVal != nil {
		return false
	}

	switch toolName {
	case "write_file", "write":
		_, hasBytesWritten := data["bytes_written"]
		return hasBytesWritten
	case "patch":
		success, ok := data["success"]
		return ok && success == true
	}

	return false
}

func IsToolResultError(toolName string, result any) bool {
	resultStr, ok := result.(string)
	if !ok {
		return false
	}
	data := make(map[string]any)
	if err := json.Unmarshal([]byte(strings.TrimSpace(resultStr)), &data); err != nil {
		return false
	}
	errVal, hasErr := data["error"]
	if hasErr && errVal != nil {
		errStr, ok := errVal.(string)
		if ok && strings.TrimSpace(errStr) != "" {
			return true
		}
	}
	return false
}
