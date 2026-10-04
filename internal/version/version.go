// Package version reports the rota build: release builds get Version, Commit
// and Date through -ldflags -X; `go install` and `go build` builds fall back
// to the module version and VCS stamp in the embedded build info.
package version

import (
	"runtime"
	"runtime/debug"
)

// Set at link time: -X github.com/l4ci/rota/internal/version.Version=5.0.0
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info is what `rota version` prints.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
}

// Get resolves the build info, preferring link-time values.
func Get() Info {
	return resolve(Version, Commit, Date, readBuildInfo())
}

func readBuildInfo() *debug.BuildInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return bi
}

func resolve(ver, commit, date string, bi *debug.BuildInfo) Info {
	info := Info{Version: ver, Commit: commit, Date: date, GoVersion: runtime.Version()}
	if bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = trimV(bi.Main.Version)
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = s.Value
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if len(info.Commit) > 7 {
		info.Commit = info.Commit[:7]
	}
	return info
}

func trimV(v string) string {
	if len(v) > 1 && v[0] == 'v' {
		return v[1:]
	}
	return v
}
