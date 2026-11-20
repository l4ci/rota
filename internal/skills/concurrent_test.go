package skills

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Both children use the public install/uninstall operations against one root.
// Each operation must leave either a complete installation or no managed files.
func TestTwoProcessInstallUninstall(t *testing.T) {
	if root := os.Getenv("ROTA_SKILLS_RACE_ROOT"); root != "" {
		s := loadFake(t, nil)
		roots := []Root{{Path: root, Agent: Claude, Scope: User}}
		fmt.Println("ready")
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
		for range 12 {
			var err error
			if os.Getenv("ROTA_SKILLS_RACE_OP") == "install" {
				_, err = s.Install(roots, Options{Mode: ModeFull})
			} else {
				_, err = Uninstall(roots, Options{Mode: ModeFull})
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if os.Getenv("ROTA_SKILLS_RACE_OP") == "install" {
			// Finish with an install after the parent has observed uninstall exit.
			if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Install(roots, Options{Mode: ModeFull}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	s := loadFake(t, nil)
	roots := oneRoot(t)
	if _, err := s.Install(roots, Options{Mode: ModeFull}); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(roots[0].Path, ManifestName+".lock")
	original, err := os.Open(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	before, err := original.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type child struct {
		cmd     *exec.Cmd
		input   io.WriteCloser
		output  *bytes.Buffer
		stderr  *bytes.Buffer
		drained <-chan struct{}
	}
	var children []child
	for _, op := range []string{"install", "uninstall"} {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTwoProcessInstallUninstall$")
		cmd.Env = append(os.Environ(), "ROTA_SKILLS_RACE_ROOT="+roots[0].Path, "ROTA_SKILLS_RACE_OP="+op)
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		buf := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill() })
		reader := bufio.NewReader(output)
		if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
			t.Fatalf("%s child readiness: %q %v", op, line, err)
		}
		// Test diagnostics remain small; drain them before Wait closes the pipe.
		drained := make(chan struct{})
		go func() { io.Copy(buf, reader); close(drained) }()
		children = append(children, child{cmd, input, buf, stderr, drained})
		t.Cleanup(func() { <-drained })
	}
	for _, c := range children {
		if _, err := c.input.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	// The installer remains alive until the uninstaller finishes, then makes
	// the final manifest deterministic while still using only two processes.
	for _, i := range []int{1, 0} {
		c := children[i]
		if i == 0 {
			if _, err := c.input.Write([]byte("f")); err != nil {
				t.Fatal(err)
			}
		}
		c.input.Close()
		err := c.cmd.Wait()
		<-c.drained
		if err != nil {
			t.Fatalf("child: %v\n%s\n%s", err, c.output, c.stderr)
		}
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("install/uninstall replaced the serialization inode: %v", err)
	}
	m, installed, _ := ReadManifest(roots[0].Path)
	if !installed || len(m.Files) != len(s.Paths()) || m.Digest != s.Digest() {
		t.Fatalf("missing or incomplete manifest: %+v", m)
	}
	for _, p := range s.Paths() {
		b, err := os.ReadFile(filepath.Join(roots[0].Path, filepath.FromSlash(p)))
		want, _ := s.File(p)
		if err != nil || !bytes.Equal(b, want) || m.Files[p] != hashBytes(want) {
			t.Errorf("manifest/file mismatch %s: %v", p, err)
		}
	}
}
