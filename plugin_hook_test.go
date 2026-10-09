package rota

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The plugin's SessionStart hook prints one systemMessage when the rota binary
// is missing or another X.Y.Z than the plugin, and nothing otherwise: not for
// a matching or dev binary, nor when plugin.json cannot be read. plugin.json's
// layout must not matter, and the hook never writes to stderr.
func TestPluginSessionStartHook(t *testing.T) {
	script, err := filepath.Abs("plugin/hooks/session-start.sh")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	pretty := "{\n  \"name\": \"rota\",\n  \"version\": \"1.2.3\"\n}\n"
	minified := `{"name":"rota","description":"x","version" : "1.2.3","keywords":["a"]}`
	for _, tc := range []struct {
		name, manifest, rota, want string // rota "" = not on PATH; manifest "" = no plugin.json
	}{
		{"missing binary", pretty, "", "need the rota binary"},
		{"mismatch", pretty, "rota 1.2.2 (abc, t)", "rota 1.2.2 does not match the rota plugin 1.2.3"},
		{"equal", pretty, "rota 1.2.3 (abc, t)", ""},
		{"dev build", pretty, "rota dev", ""},
		{"minified mismatch", minified, "rota 1.2.2 (abc, t)", "rota 1.2.2 does not match the rota plugin 1.2.3"},
		{"minified equal", minified, "rota 1.2.3 (abc, t)", ""},
		{"unreadable plugin.json", "", "rota 1.2.2 (abc, t)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, bin := t.TempDir(), t.TempDir()
			if tc.manifest != "" {
				os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755)
				os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(tc.manifest), 0o644)
			}
			if tc.rota != "" {
				os.WriteFile(filepath.Join(bin, "rota"), []byte("#!/bin/sh\necho '"+tc.rota+"'\n"), 0o755)
			}
			// PATH holds the fake rota and the tools the hook uses, never a real rota.
			tools := t.TempDir()
			for _, tool := range []string{"sed", "awk", "head", "tr"} {
				if p, err := exec.LookPath(tool); err == nil {
					os.Symlink(p, filepath.Join(tools, tool))
				}
			}
			cmd := exec.Command(sh, script)
			cmd.Env = []string{"PATH=" + bin + ":" + tools, "CLAUDE_PLUGIN_ROOT=" + root}
			var out, errOut bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errOut
			if err := cmd.Run(); err != nil {
				t.Fatalf("exit: %v (stderr %q)", err, errOut.String())
			}
			if errOut.Len() > 0 {
				t.Errorf("stderr: %q", errOut.String())
			}
			got := strings.TrimSpace(out.String())
			switch {
			case tc.want == "" && got != "":
				t.Errorf("want silence, got %q", got)
			case tc.want != "" && !(strings.HasPrefix(got, `{"systemMessage": "`) && strings.Contains(got, tc.want) && strings.Count(got, "\n") == 0):
				t.Errorf("want one systemMessage line with %q, got %q", tc.want, got)
			}
		})
	}
}
