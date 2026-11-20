package appdata

import (
	"bytes"
	"errors"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Compose files: moving a stack's named volumes into App data rewrites the
// services' volume entries into folders, and drops the volumes it no longer
// uses from the top-level list. Comments and order are kept.

func parseCompose(text string) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("the compose file isn't a YAML mapping")
	}
	return &doc, doc.Content[0], nil
}

// mapValue returns the value of key in a mapping node (nil when missing).
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// serviceVolumes calls fn with every volume entry of every service.
func serviceVolumes(top *yaml.Node, fn func(entry *yaml.Node)) {
	services := mapValue(top, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return
	}
	for i := 1; i < len(services.Content); i += 2 {
		vols := mapValue(services.Content[i], "volumes")
		if vols == nil || vols.Kind != yaml.SequenceNode {
			continue
		}
		for _, e := range vols.Content {
			fn(e)
		}
	}
}

// RewriteComposeVolumes turns the named volumes in dirs (compose volume key
// → folder) into folders on the server.
func RewriteComposeVolumes(text string, dirs map[string]string) (string, error) {
	doc, top, err := parseCompose(text)
	if err != nil {
		return "", err
	}
	serviceVolumes(top, func(e *yaml.Node) {
		switch e.Kind {
		case yaml.ScalarNode:
			key, rest, ok := strings.Cut(e.Value, ":")
			if dir, moved := dirs[key]; ok && moved {
				e.Value = dir + ":" + rest
				e.Style = 0
			}
		case yaml.MappingNode:
			src := mapValue(e, "source")
			typ := mapValue(e, "type")
			if src == nil {
				return
			}
			if dir, moved := dirs[src.Value]; moved && (typ == nil || typ.Value == "volume") {
				src.Value = dir
				if typ != nil {
					typ.Value = "bind"
				} else {
					e.Content = append([]*yaml.Node{
						{Kind: yaml.ScalarNode, Value: "type"}, {Kind: yaml.ScalarNode, Value: "bind"},
					}, e.Content...)
				}
				deleteKey(e, "volume") // volume options don't apply to folders
			}
		}
	})
	if vols := mapValue(top, "volumes"); vols != nil && vols.Kind == yaml.MappingNode {
		for key := range dirs {
			deleteKey(vols, key)
		}
		if len(vols.Content) == 0 {
			deleteKey(top, "volumes")
		}
	}
	return encode(doc)
}

func encode(doc *yaml.Node) (string, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ComposeBinds lists the folders on the server a compose file gives its
// services (full paths only; relative ones stay in the stack's folder).
func ComposeBinds(text string) ([]string, error) {
	_, top, err := parseCompose(text)
	if err != nil {
		return nil, err
	}
	var out []string
	serviceVolumes(top, func(e *yaml.Node) {
		switch e.Kind {
		case yaml.ScalarNode:
			if src, _, ok := strings.Cut(e.Value, ":"); ok && strings.HasPrefix(src, "/") {
				out = append(out, src)
			}
		case yaml.MappingNode:
			if src := mapValue(e, "source"); src != nil && strings.HasPrefix(src.Value, "/") {
				out = append(out, src.Value)
			}
		}
	})
	return out, nil
}

// ReplaceRoot points a compose file's folders inside the old App data
// folder at the new one.
func ReplaceRoot(text, oldRoot, newRoot string) string {
	oldRoot, newRoot = strings.TrimSuffix(oldRoot, "/"), strings.TrimSuffix(newRoot, "/")
	return strings.ReplaceAll(text, oldRoot+"/", newRoot+"/")
}
