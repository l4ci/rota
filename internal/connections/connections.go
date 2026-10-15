// Package connections is the machine-wide registry of tracker connections
// (Jira site, Linear workspace) and the secrets behind them. Connections live
// in connections.json under the global rota config dir, beside projects.json;
// a project only names one (issues.connection). The file holds no secret:
// tokens go to the OS keychain, else to a 0600 file in the same dir, and
// ROTA_<NAME>_TOKEN overrides both.
package connections

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Kinds a connection can have.
const (
	KindJira   = "jira"
	KindLinear = "linear"
)

// Kinds lists the accepted kinds, for messages.
var Kinds = []string{KindJira, KindLinear}

// ErrExists is Add refusing to overwrite a connection without replace.
var ErrExists = errors.New("connection already exists")

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Connection is one named tracker connection. Nothing here is secret.
type Connection struct {
	Name, Kind, Site, User string
}

// Path is the connections file under dir, the global rota config dir.
func Path(dir string) string { return filepath.Join(dir, "connections.json") }

// Validate reports why c cannot be stored, or nil.
func (c Connection) Validate() error {
	if !nameRE.MatchString(c.Name) {
		return fmt.Errorf("invalid connection name %q: use letters, digits, - and _", c.Name)
	}
	switch c.Kind {
	case KindJira, KindLinear:
	default:
		return fmt.Errorf("unknown kind %q (expected %s)", c.Kind, strings.Join(Kinds, "|"))
	}
	if c.Kind == KindJira && !strings.HasPrefix(c.Site, "https://") && !strings.HasPrefix(c.Site, "http://") {
		return fmt.Errorf("a jira connection needs --site, an http(s) URL such as https://acme.atlassian.net")
	}
	return nil
}

// Load is every connection, sorted by name. A missing file is none; a file
// that does not parse is an error, never silently dropped.
func Load(dir string) ([]Connection, error) {
	raw, err := os.ReadFile(Path(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %v", Path(dir), err)
	}
	out := decode(v)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// errNoWrite aborts an UpdateJSONStrict mutate that has nothing to write.
var errNoWrite = errors.New("no write")

// Get is the connection called name.
func Get(dir, name string) (Connection, bool, error) {
	cs, err := Load(dir)
	if err != nil {
		return Connection{}, false, err
	}
	for _, c := range cs {
		if c.Name == name {
			return c, true, nil
		}
	}
	return Connection{}, false, nil
}

// Add stores c. An existing name is ErrExists unless replace is set.
func Add(dir string, c Connection, replace bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return fsio.UpdateJSONStrict(Path(dir), jsonx.NewObject(), func(v any) (any, error) {
		items := decodeRaw(v)
		kept := []any{}
		for _, it := range items {
			if o, ok := it.(*jsonx.Object); ok && jsonx.Str(o, "name") == c.Name {
				if !replace {
					return nil, fmt.Errorf("%w: %s", ErrExists, c.Name)
				}
				continue
			}
			kept = append(kept, it)
		}
		o := jsonx.NewObject()
		o.Set("name", c.Name)
		o.Set("kind", c.Kind)
		o.Set("site", c.Site)
		o.Set("user", c.User)
		doc := jsonx.NewObject()
		doc.Set("connections", append(kept, o))
		return doc, nil
	})
}

// Remove drops the connection called name and says whether it existed.
func Remove(dir, name string) (bool, error) {
	found := false
	if _, err := os.Stat(Path(dir)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	err := fsio.UpdateJSONStrict(Path(dir), jsonx.NewObject(), func(v any) (any, error) {
		kept := []any{}
		for _, it := range decodeRaw(v) {
			if o, ok := it.(*jsonx.Object); ok && jsonx.Str(o, "name") == name {
				found = true
				continue
			}
			kept = append(kept, it)
		}
		if !found {
			return nil, errNoWrite
		}
		doc := jsonx.NewObject()
		doc.Set("connections", kept)
		return doc, nil
	})
	if errors.Is(err, errNoWrite) {
		return false, nil
	}
	return found, err
}

func decodeRaw(v any) []any {
	doc, _ := v.(*jsonx.Object)
	if doc == nil {
		return nil
	}
	raw, _ := doc.Get("connections")
	items, _ := raw.([]any)
	return items
}

func decode(v any) []Connection {
	var out []Connection
	for _, it := range decodeRaw(v) {
		if o, ok := it.(*jsonx.Object); ok {
			out = append(out, Connection{Name: jsonx.Str(o, "name"), Kind: jsonx.Str(o, "kind"), Site: jsonx.Str(o, "site"), User: jsonx.Str(o, "user")})
		}
	}
	return out
}
