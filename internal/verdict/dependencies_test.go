package verdict

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Verdict records must remain usable without the backlog and tracker stack.
func TestVerdictDoesNotImportBacklog(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "github.com/l4ci/rota/internal/backlog" || strings.HasPrefix(path, "github.com/l4ci/rota/internal/backlog/") {
				t.Errorf("%s imports %s; follow-up filing belongs outside verdict", entry.Name(), path)
			}
		}
	}
}
