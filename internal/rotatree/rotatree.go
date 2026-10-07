// Package rotatree is the one place that knows the in-tree .rota/ layout of a
// project: the directory name and the names of the files and directories
// under it. Packages ask it for a path instead of joining ".rota" themselves.
// The per-checkout runtime state under the git common dir is rotastate's.
package rotatree

import (
	"os"
	"path/filepath"
)

// DirName is the state directory at a project root.
const DirName = ".rota"

// Names of the files and directories under .rota/.
const (
	ConfigFile      = "config.json"
	ConfigLocalFile = "config.local.json"
	WorkersFile     = "workers.json"
	ReposFile       = "repos.json"
	StatusFile      = "status.json"
	VerdictsFile    = "verdicts.json"
	TrainCacheFile  = "train-cache.json"
	CountersFile    = "counters.json"
	IssueMapFile    = "issue-map.json"
	BacklogFile     = "BACKLOG.md"
	ArchiveFile     = "ARCHIVE.md"
	KnowledgeFile   = "KNOWLEDGE.md"
	DecisionsFile   = "DECISIONS.md"
	MilestonesFile  = "MILESTONES.md"

	DesignsDir    = "designs"
	PlansDir      = "plans"
	SpikesDir     = "spikes"
	MilestonesDir = "milestones"
	MapDir        = "map"
	QADir         = "qa"
	KnowledgeDir  = "knowledge"
	DebugDir      = "debug"
)

// Dir is the .rota directory of the project at root.
func Dir(root string) string { return filepath.Join(root, DirName) }

// File joins parts under the project's .rota directory.
func File(root string, parts ...string) string {
	return filepath.Join(append([]string{root, DirName}, parts...)...)
}

// Config is the tracked config.json.
func Config(root string) string { return File(root, ConfigFile) }

// ConfigLocal is the per-developer config overlay.
func ConfigLocal(root string) string { return File(root, ConfigLocalFile) }

// Workers is the worker slot registry.
func Workers(root string) string { return File(root, WorkersFile) }

// Repos is the umbrella's sub-repo registry.
func Repos(root string) string { return File(root, ReposFile) }

// Status is the status.json runtime file.
func Status(root string) string { return File(root, StatusFile) }

// Verdicts is the review verdict store.
func Verdicts(root string) string { return File(root, VerdictsFile) }

// TrainCache is the merge train verdict cache (gitignored).
func TrainCache(root string) string { return File(root, TrainCacheFile) }

// Backlog is BACKLOG.md.
func Backlog(root string) string { return File(root, BacklogFile) }

// Decisions is the umbrella DECISIONS.md.
func Decisions(root string) string { return File(root, DecisionsFile) }

// Milestones is the MILESTONES.md overview.
func Milestones(root string) string { return File(root, MilestonesFile) }

// IssueMap is the backlog-ID to issue-number map.
func IssueMap(root string) string { return File(root, IssueMapFile) }

// Doc is the markdown file name under a directory such as PlansDir.
func Doc(root, dir, name string) string { return File(root, dir, name+".md") }

// Exists reports whether root has a .rota directory.
func Exists(root string) bool { return isDir(Dir(root)) }

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
