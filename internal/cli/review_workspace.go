package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
)

const reviewWorkspacePrefix = "rota-review-"
const reviewWorkspaceMarker = "workspace.json"

type reviewWorkspace struct {
	Path string `json:"path"`
	Head string `json:"head"`
}

func reviewPrepare(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 || args[0] == "" {
		return Result{}, Usage("usage: rota review prepare <commit>")
	}
	dir, err := gitDir(c)
	if err != nil {
		return Result{}, err
	}
	path, head, err := reviewPrepareWorkspace(c, dir, args[0])
	if err != nil {
		return Result{}, err
	}
	return Result{Data: jsonx.Obj("changed", true, "path", path, "head", head), Text: path}, nil
}

func reviewPrepareWorkspace(c *Ctx, dir, ref string) (path, head string, err error) {
	// Resolve once, then archive only that object even if the requested ref moves.
	out, err := reviewGit(c.Context(), c.deps(), dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", "", Resolution("%s: cannot resolve commit: %v", ref, err)
	}
	head = strings.TrimSpace(out)
	// Canonical paths let cleanup reject symlinked parents rather than following them.
	temp, err := reviewWorkspaceTempDir(c.deps().TempDir())
	if err != nil {
		return "", "", Unavailable("resolve temporary directory: %v", err)
	}
	root, err := os.MkdirTemp(temp, reviewWorkspacePrefix)
	if err != nil {
		return "", "", Unavailable("create review workspace: %v", err)
	}
	defer func() {
		if err != nil {
			if cleanErr := c.deps().ReviewRemoveAll(root); cleanErr != nil {
				err = Unavailable("%v; cleanup failed for %s: %v", err, root, cleanErr)
			}
		}
	}()
	path = filepath.Join(root, "tree")
	archive := filepath.Join(root, "source.tar")
	if _, err = reviewGit(c.Context(), c.deps(), dir, "archive", "--format=tar", "--output="+archive, head); err != nil {
		return "", "", err
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		return "", "", Unavailable("create review tree: %v", err)
	}
	res, runErr := c.deps().Proc(c.Context(), proc.Cmd{Name: "tar", Args: []string{"-xf", archive, "-C", path}, Dir: root})
	if runErr != nil || res.ExitCode != 0 {
		return "", "", Unavailable("extract review archive: %v (exit %d): %s", runErr, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	// A tracked .review (including a symlink) cannot masquerade as setup inputs.
	if err = os.Mkdir(filepath.Join(path, ".review"), 0o700); err != nil {
		return "", "", Unavailable("set up review inputs: %v", err)
	}
	if err = os.Remove(archive); err != nil {
		return "", "", Unavailable("remove review archive: %v", err)
	}
	marker, err := json.Marshal(reviewWorkspace{Path: path, Head: head})
	if err != nil {
		return "", "", err
	}
	if err = os.WriteFile(filepath.Join(root, reviewWorkspaceMarker), marker, 0o600); err != nil {
		return "", "", Unavailable("complete review workspace: %v", err)
	}
	return path, head, nil
}

func reviewCleanup(c *Ctx, args []string) (Result, error) {
	if len(args) != 1 {
		return Result{}, Usage("usage: rota review cleanup <workspace>")
	}
	path := filepath.Clean(args[0])
	if !filepath.IsAbs(path) || filepath.Base(path) != "tree" {
		return Result{}, Refused("not a prepared review workspace: %s", path)
	}
	temp, err := reviewWorkspaceTempDir(c.deps().TempDir())
	if err != nil {
		return Result{}, Unavailable("resolve temporary directory: %v", err)
	}
	// Resolve only the workspace's parent: a symlinked ancestor (macOS /var ->
	// /private/var) is fine, a symlinked workspace dir is not.
	wsDir := filepath.Dir(path)
	parent, err := filepath.EvalSymlinks(filepath.Dir(wsDir))
	if err != nil || parent != temp || !strings.HasPrefix(filepath.Base(wsDir), reviewWorkspacePrefix) {
		return Result{}, Refused("not a prepared review workspace: %s", path)
	}
	root := filepath.Join(parent, filepath.Base(wsDir))
	if canonical, err := filepath.EvalSymlinks(root); err != nil || canonical != root {
		return Result{}, Refused("not a prepared review workspace: %s", path)
	}
	path = filepath.Join(root, "tree")
	if ti, err := os.Lstat(path); err != nil || !ti.IsDir() {
		return Result{}, Refused("not a prepared review workspace: %s", path)
	}
	info, err := os.Lstat(filepath.Join(root, reviewWorkspaceMarker))
	if err != nil || !info.Mode().IsRegular() {
		return Result{}, Refused("missing review workspace ownership record: %s", path)
	}
	marker, found, err := fsio.ReadJSON[reviewWorkspace](filepath.Join(root, reviewWorkspaceMarker))
	if err != nil || !found || marker.Path != path || marker.Head == "" {
		return Result{}, Refused("invalid review workspace ownership record: %s", path)
	}
	// RemoveAll removes symlinks inside the tree themselves; it never follows them.
	if err = c.deps().ReviewRemoveAll(root); err != nil {
		return Result{}, Unavailable("cleanup review workspace %s: %v", path, err)
	}
	return Result{Data: jsonx.Obj("changed", true, "path", path, "head", marker.Head), Text: fmt.Sprintf("removed %s", path)}, nil
}

func reviewWorkspaceTempDir(dir string) (string, error) {
	temp, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(temp)
}
