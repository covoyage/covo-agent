package cyber

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type OperationShape struct {
	Key        string   `json:"key"`
	Template   string   `json:"template"`
	Protocol   string   `json:"protocol"`
	Parameters []string `json:"parameters"`
	Complete   bool     `json:"complete"`
}

func operationForExchange(ex Exchange, parsed *url.URL) OperationShape {
	segments := strings.Split(parsed.EscapedPath(), "/")
	for index, segment := range segments {
		if identifierValue(segment) {
			segments[index] = "{id}"
		}
	}
	shape := OperationShape{
		Template: strings.ToUpper(ex.Method) + " " + strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + strings.Join(segments, "/"),
		Protocol: "http", Complete: !ex.HeadersTruncated && !ex.RequestTruncated && !ex.Redacted,
	}
	parameters := map[string]bool{}
	discriminators := []string{}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		shape.Complete = false
	}
	for name, values := range query {
		parameters[fmt.Sprintf("query:%q:%d", name, len(values))] = true
		switch strings.ToLower(name) {
		case "action", "operation", "operationname", "method", "query":
			data, _ := json.Marshal([]any{name, values})
			discriminators = append(discriminators, string(data))
		}
	}
	discriminator := ""
	if strings.TrimSpace(ex.Request) != "" {
		var body any
		decoder := json.NewDecoder(strings.NewReader(ex.Request))
		decoder.UseNumber()
		if json.Valid([]byte(ex.Request)) && decoder.Decode(&body) == nil {
			count := 0
			shape.Complete = shape.Complete && operationFields(body, "", 0, parameters, &count)
			if object, ok := body.(map[string]any); ok {
				for _, name := range []string{"action", "operation", "operationName", "method"} {
					if value, present := object[name]; present {
						data, _ := json.Marshal([]any{name, value})
						discriminators = append(discriminators, string(data))
					}
				}
				if queryText, ok := object["query"].(string); ok {
					shape.Protocol = "graphql"
					discriminator = queryText + "\x00" + fmt.Sprint(object["operationName"])
				} else if version, ok := object["jsonrpc"].(string); ok && version == "2.0" {
					shape.Protocol = "jsonrpc"
					method, valid := object["method"].(string)
					shape.Complete = shape.Complete && valid && method != ""
					discriminator = method
				}
			} else {
				shape.Complete = false
			}
		} else if form, err := url.ParseQuery(ex.Request); err == nil && strings.Contains(ex.Request, "=") {
			for name, values := range form {
				parameters[fmt.Sprintf("form:%q:%d", name, len(values))] = true
				switch strings.ToLower(name) {
				case "action", "operation", "operationname", "method", "query":
					data, _ := json.Marshal([]any{name, values})
					discriminators = append(discriminators, string(data))
				}
			}
		} else {
			shape.Complete = false
		}
	}
	if len(parameters) > 128 {
		shape.Complete = false
	}
	for parameter := range parameters {
		if len(parameter) > 512 {
			shape.Complete = false
		}
		shape.Parameters = append(shape.Parameters, parameter)
	}
	sort.Strings(shape.Parameters)
	sort.Strings(discriminators)
	if !shape.Complete {
		shape.Parameters = []string{}
		discriminators = []string{}
		shape.Template = ex.Method + " " + parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
		discriminator = ex.Request + "\x00" + parsed.RawQuery
	}
	data, _ := json.Marshal([]any{shape.Template, shape.Protocol, shape.Parameters, discriminator, discriminators, ex.HeadersTruncated, ex.RequestTruncated, ex.Redacted})
	shape.Key = fmt.Sprintf("%x", sha256.Sum256(data))
	return shape
}

func operationFields(value any, path string, depth int, fields map[string]bool, count *int) bool {
	*count = *count + 1
	if depth > 8 || *count > 128 || len(path) > 512 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		fields["body:"+path+":object"] = true
		for name, child := range typed {
			escaped := strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
			if !operationFields(child, path+"/"+escaped, depth+1, fields, count) {
				return false
			}
		}
	case []any:
		fields["body:"+path+":array"] = true
		for _, child := range typed {
			if !operationFields(child, path+"/[]", depth+1, fields, count) {
				return false
			}
		}
	default:
		fields[fmt.Sprintf("body:%s:%T", path, value)] = true
	}
	return true
}

func identifierValue(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	if len(value) == 36 {
		parsed, err := uuid.Parse(value)
		if err == nil && strings.EqualFold(parsed.String(), value) {
			return true
		}
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
