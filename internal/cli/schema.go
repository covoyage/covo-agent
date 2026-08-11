package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type jsonSchema struct {
	Schema      string                `json:"$schema,omitempty"`
	Title       string                `json:"title,omitempty"`
	Type        string                `json:"type,omitempty"`
	Properties  map[string]jsonSchema `json:"properties,omitempty"`
	Items       *jsonSchema           `json:"items,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Additional  *bool                 `json:"additionalProperties,omitempty"`
	OneOf       []jsonSchema          `json:"oneOf,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Ref         string                `json:"$ref,omitempty"`
	Definitions map[string]jsonSchema `json:"$defs,omitempty"`
	Description string                `json:"description,omitempty"`
	Default     interface{}           `json:"default,omitempty"`
}

func GenerateConfigSchema() (string, error) {
	t := reflect.TypeOf(Config{})
	schema := buildSchema(t, nil)
	schema.Schema = "https://json-schema.org/draft-07/schema#"
	schema.Title = "Covo Agent Configuration"

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func buildSchema(t reflect.Type, seen map[string]bool) jsonSchema {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		if seen == nil {
			seen = make(map[string]bool)
		}
		typeName := t.PkgPath() + "." + t.Name()
		if typeName != "." && seen[typeName] {
			return jsonSchema{Ref: "#/$defs/" + t.Name()}
		}
		if typeName != "." {
			seen[typeName] = true
		}

		s := jsonSchema{
			Type:       "object",
			Properties: make(map[string]jsonSchema),
		}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts := parseYamlTag(f.Tag.Get("yaml"))
			if name == "" || name == "-" {
				continue
			}
			prop := typeToSchema(f.Type, seen)
			if d := f.Tag.Get("yaml"); d != "" {
				parts := strings.Split(d, ",")
				if len(parts) > 0 && parts[0] != "" && parts[0] != "-" {
					prop.Description = fmt.Sprintf("yaml key: %s", parts[0])
				}
			}
			s.Properties[name] = prop
			if !opts["omitempty"] {
				required = append(required, name)
			}
		}
		if len(required) > 0 {
			s.Required = required
		}
		return s
	}
	if t.Kind() == reflect.Slice {
		items := buildSchema(t.Elem(), seen)
		return jsonSchema{
			Type:  "array",
			Items: &items,
		}
	}
	if t.Kind() == reflect.Map {
		additional := true
		return jsonSchema{
			Type:       "object",
			Additional: &additional,
		}
	}
	return jsonSchema{Type: jsonTypeName(t)}
}

func typeToSchema(t reflect.Type, seen map[string]bool) jsonSchema {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		return buildSchema(t, seen)
	}
	if t.Kind() == reflect.Slice {
		items := typeToSchema(t.Elem(), seen)
		s := jsonSchema{Type: "array", Items: &items}
		return s
	}
	if t.Kind() == reflect.Map {
		additional := true
		return jsonSchema{
			Type:       "object",
			Additional: &additional,
		}
	}
	return jsonSchema{Type: jsonTypeName(t)}
}

func jsonTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	default:
		return "string"
	}
}

func parseYamlTag(tag string) (name string, opts map[string]bool) {
	opts = make(map[string]bool)
	if tag == "" {
		return "", opts
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, p := range parts[1:] {
		opts[p] = true
	}
	return name, opts
}
