package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
	"github.com/l4ci/rota/internal/rotatree"
)

// validateType checks JSON shapes, leaving enum choices and key-specific rules
// to validateValue. ship.review also supports legacy booleans and policy objects.
func validateType(key string, value any) error {
	k, ok := SchemaKey(key)
	if !ok {
		return nil
	}
	if key == ReviewKey {
		switch value.(type) {
		case nil, string, bool, *jsonx.Object:
			return nil
		}
		return fmt.Errorf("%w for %s: expected enum string, bool or object", ErrBadValue, key)
	}
	var matches bool
	switch k.Type {
	case TypeBool:
		_, matches = value.(bool)
	case TypeInt:
		if n, ok := value.(json.Number); ok {
			_, err := n.Int64()
			matches = err == nil
		}
	case TypeList:
		_, matches = value.([]any)
	case TypeObject:
		_, matches = value.(*jsonx.Object)
	case TypeString, TypePath, TypeEnum:
		_, matches = value.(string)
	}
	if matches {
		return nil
	}
	hint := ""
	if k.Type == TypeEnum {
		hint = "; choices: " + strings.Join(k.Choices, ", ")
	}
	if k.Type == TypeList {
		example := "command"
		if s, ok := value.(string); ok {
			example = s
		}
		b, _ := jsonx.MarshalCompact([]any{example})
		hint = "; use a JSON list, for example " + string(b)
	}
	return fmt.Errorf("%w for %s: expected %s%s", ErrBadValue, key, k.Type, hint)
}

// TypeProblems reports non-null values with a different JSON shape from their
// schema key, including values hidden by a higher layer. Null means unset.
// It only diagnoses stored values; it never rewrites them.
func TypeProblems(env projects.Env, root string) []string {
	out := []string{}
	for _, layer := range []struct {
		name string
		doc  any
	}{
		{"global config", globalLayer(env)},
		{rotatree.Rel(rotatree.ConfigFile), fsio.LoadJSON(configPath(root), nil)},
		{rotatree.Rel(rotatree.ConfigLocalFile), fsio.LoadJSON(localPath(root), nil)},
	} {
		for _, k := range Keys {
			if value, present := walk(layer.doc, k.Name); present {
				if err := validateType(k.Name, value); err != nil {
					out = append(out, fmt.Sprintf("%s: %v", layer.name, err))
				}
			}
		}
	}
	return out
}
