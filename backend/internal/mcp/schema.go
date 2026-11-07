package mcp

import (
	"fmt"
	"math"
	"sort"
)

// Prop is one tool argument.
type Prop struct {
	Name        string
	Type        string // string | integer | number | boolean
	Description string
	Required    bool
	Enum        []string
}

// Schema builds a JSON Schema object for a tool's arguments.
func Schema(props ...Prop) map[string]any {
	ps := map[string]any{}
	required := []string{}
	for _, p := range props {
		d := map[string]any{"type": p.Type, "description": p.Description}
		if len(p.Enum) > 0 {
			d["enum"] = p.Enum
		}
		ps[p.Name] = d
		if p.Required {
			required = append(required, p.Name)
		}
	}
	s := map[string]any{"type": "object", "properties": ps, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// ValidateArgs checks args against a schema made by Schema: required
// arguments are there, nothing unknown is, and each has its type (and, for
// enums, one of the allowed values).
func ValidateArgs(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	switch req := schema["required"].(type) {
	case []string:
		for _, name := range req {
			if _, ok := args[name]; !ok {
				return fmt.Errorf("%q is required", name)
			}
		}
	case []any:
		for _, n := range req {
			name, _ := n.(string)
			if _, ok := args[name]; !ok {
				return fmt.Errorf("%q is required", name)
			}
		}
	}
	names := make([]string, 0, len(args))
	for k := range args {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		d, ok := props[name].(map[string]any)
		if !ok {
			return fmt.Errorf("unknown argument %q", name)
		}
		v := args[name]
		typ, _ := d["type"].(string)
		if !hasType(v, typ) {
			return fmt.Errorf("%q must be a %s", name, typ)
		}
		if enum, ok := d["enum"].([]string); ok {
			s, _ := v.(string)
			found := false
			for _, e := range enum {
				found = found || e == s
			}
			if !found {
				return fmt.Errorf("%q must be one of %v", name, enum)
			}
		}
	}
	return nil
}

func hasType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	}
	return true
}

// Str is a string argument (empty when missing).
func Str(args map[string]any, name string) string {
	s, _ := args[name].(string)
	return s
}

// Int is an integer argument, def when missing.
func Int(args map[string]any, name string, def int) int {
	if f, ok := args[name].(float64); ok {
		return int(f)
	}
	return def
}

// Bool is a boolean argument (false when missing).
func Bool(args map[string]any, name string) bool {
	b, _ := args[name].(bool)
	return b
}
