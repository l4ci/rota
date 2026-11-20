package frontmatter

import (
	"fmt"
	"regexp"
	"strings"
)

// Field is one front-matter entry in file order. Value is a string, a
// []string (written as an inline list) or Optional.
type Field struct {
	Key   string
	Value any
}

// Optional is a string value that is left out when empty.
type Optional string

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Render writes fields as a `---` block that Parse reads back unchanged. The
// subset has no quoting, so a value Parse could not return verbatim is refused
// instead of escaped: a line break (it would add keys), surrounding space
// (Parse trims it), or a string wrapped in [ ] (Parse reads it as a list).
// List items additionally may not hold a comma. Plain strings may be empty.
func Render(fields []Field) (string, error) {
	var b strings.Builder
	b.WriteString("---\n")
	for _, f := range fields {
		if !keyRe.MatchString(f.Key) {
			return "", fmt.Errorf("front-matter key %q is not a plain word", f.Key)
		}
		switch v := f.Value.(type) {
		case Optional:
			if v == "" {
				continue
			}
			if err := checkScalar(f.Key, string(v)); err != nil {
				return "", err
			}
			b.WriteString(f.Key + ": " + string(v) + "\n")
		case string:
			if err := checkScalar(f.Key, v); err != nil {
				return "", err
			}
			b.WriteString(f.Key + ": " + v + "\n")
		case []string:
			for _, item := range v {
				if err := checkScalar(f.Key, item); err != nil {
					return "", err
				}
				if item == "" || strings.Contains(item, ",") {
					return "", fmt.Errorf("front-matter %s: list item %q must be non-empty and hold no comma", f.Key, item)
				}
			}
			b.WriteString(f.Key + ": [" + strings.Join(v, ", ") + "]\n")
		default:
			return "", fmt.Errorf("front-matter %s: unsupported value type %T", f.Key, f.Value)
		}
	}
	b.WriteString("---\n")
	return b.String(), nil
}

func checkScalar(key, v string) error {
	switch {
	case strings.ContainsAny(v, "\r\n"):
		return fmt.Errorf("front-matter %s must be one line (no line break)", key)
	case v != strings.TrimSpace(v):
		return fmt.Errorf("front-matter %s must not start or end with whitespace", key)
	case strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]"):
		return fmt.Errorf("front-matter %s must not be wrapped in [ ]", key)
	}
	return nil
}
