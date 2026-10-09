package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginName is the Claude Code plugin rota ships as.
const PluginName = "rota"

// PluginsDirs maps Claude config dirs to their plugins roots: the first (the
// current one) is $CLAUDE_CODE_PLUGIN_CACHE_DIR when set, the rest
// <dir>/plugins.
func PluginsDirs(getenv func(string) string, claudeDirs []string) []string {
	var out []string
	for i, d := range claudeDirs {
		if c := getenv("CLAUDE_CODE_PLUGIN_CACHE_DIR"); i == 0 && c != "" {
			out = append(out, c)
			continue
		}
		out = append(out, filepath.Join(d, "plugins"))
	}
	return out
}

// PluginRoots lists the skill roots of the rota plugin installs recorded in
// each plugins root's installed_plugins.json. User installs always count;
// project and local ones only when their projectPath is top. The file's shape
// is undocumented, so anything unreadable yields no roots.
func PluginRoots(pluginDirs []string, top string) []Root {
	var out []Root
	seen := map[string]bool{}
	for _, dir := range pluginDirs {
		b, err := os.ReadFile(filepath.Join(dir, "installed_plugins.json"))
		if err != nil {
			continue
		}
		var f struct {
			Plugins map[string][]struct {
				Scope       string `json:"scope"`
				ProjectPath string `json:"projectPath"`
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		if json.Unmarshal(b, &f) != nil {
			continue
		}
		var keys []string
		for k := range f.Plugins {
			if name, _, _ := strings.Cut(k, "@"); name == PluginName {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, e := range f.Plugins[k] {
				scope := User
				switch e.Scope {
				case "user", "":
				case "project", "local":
					if top == "" || e.ProjectPath == "" || !sameDir(e.ProjectPath, top) {
						continue
					}
					scope = Project
				default:
					continue
				}
				if e.InstallPath == "" {
					continue
				}
				p := filepath.Join(e.InstallPath, "skills")
				if seen[p] {
					continue
				}
				seen[p] = true
				out = append(out, Root{Path: p, Agent: Claude, Scope: scope, Plugin: true})
			}
		}
	}
	return out
}

func sameDir(a, b string) bool {
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// pluginStatus reads a plugin root: no manifest, so it is current when the
// files at the set's paths hash to the set's digest.
func (s *Set) pluginStatus(st *RootStatus) {
	fi, err := os.Stat(st.Path)
	if err != nil || !fi.IsDir() {
		return
	}
	st.Installed = true
	if b, err := os.ReadFile(filepath.Join(st.Path, "..", ".claude-plugin", "plugin.json")); err == nil {
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &m) == nil {
			st.Version = m.Version
		}
	}
	h := sha256.New()
	for _, p := range s.paths {
		b, err := os.ReadFile(filepath.Join(st.Path, filepath.FromSlash(p)))
		if err != nil {
			st.Missing = append(st.Missing, p)
			continue
		}
		fmt.Fprintf(h, "%s\x00%s\n", p, hashBytes(b))
	}
	st.Digest = hex.EncodeToString(h.Sum(nil))
	st.Current = st.Digest == s.digest
}
