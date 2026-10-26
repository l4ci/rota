package toolver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name, dir string
		want      map[string]string
	}{
		{"npm", "npm", map[string]string{
			"next.js": "15.1.0", "three.js": "0.170.0", "@storybook/react": "8.4.2",
			"typescript": "6.0.3", "vite": "8.1.5", "playwright": "1.48.0",
			"graphql-codegen": "5.0.3", "react": "19.0.0", "node": "20.11",
		}},
		{"ranges", "ranges", map[string]string{"a": "4", "b": "4.1.0", "d": "2.3.4", "e": "1"}},
		{"go", "gomod", map[string]string{"go": "1.23"}},
		{"go without go line", "gonogo", map[string]string{}},
		{"invalid json", "badjson", map[string]string{}},
		{"python and uv", "py", map[string]string{
			"python": "3.12", "streamlit": "1.66.0", "plotly-express": "6.0.0", "plotly": "6.3.1",
		}},
		{"swift package", "swiftpkg", map[string]string{"swift": "6.0"}},
		{"xcodegen highest", "xcodegen", map[string]string{"swift": "6.0"}},
		{"pbxproj highest", "xcode", map[string]string{"swift": "6.0"}},
		{"nearest wins", "umbrella/sub/deep", map[string]string{"next.js": "15.2.0", "react": "18.0.0"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := filepath.Join("testdata", c.dir)
			if c.dir == "umbrella/sub/deep" {
				root = filepath.Join("testdata", "umbrella")
			}
			got := Detect(filepath.Join("testdata", c.dir), root)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Detect = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDetectUnreadableAndMissing(t *testing.T) {
	d := t.TempDir()
	// A directory named like a manifest cannot be read as a file.
	if err := os.MkdirAll(filepath.Join(d, "package.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, d, "go.mod", "go\n")
	if got := Detect(d, d); len(got) != 0 {
		t.Errorf("unreadable = %v", got)
	}
	if got := Detect(filepath.Join(d, "nope"), d); len(got) != 0 {
		t.Errorf("missing cwd = %v", got)
	}
}
