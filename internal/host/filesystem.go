package host

import (
	"os"
	"path/filepath"
	"time"

	"github.com/l4ci/rota/internal/fsio"
)

// FileSystem is the host's local file boundary. Implementations must serialize
// Locked callbacks for the same path and publish WriteFileAtomic data atomically.
type FileSystem interface {
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, os.FileMode) error
	Stat(string) (os.FileInfo, error)
	MkdirAll(string, os.FileMode) error
	// CreateTemp creates an empty, closed file and returns its path.
	CreateTemp(dir, pattern string) (string, error)
	Remove(string) error
	Glob(string) ([]string, error)
	Locked(string, time.Duration, func() error) error
	WriteFileAtomic(string, []byte) error
}

// OSFileSystem uses the operating system and fsio's process-safe persistence.
// Stand-ins can embed it and redirect paths to an isolated backing directory.
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (OSFileSystem) WriteFile(p string, b []byte, mode os.FileMode) error {
	return os.WriteFile(p, b, mode)
}
func (OSFileSystem) Stat(p string) (os.FileInfo, error)        { return os.Stat(p) }
func (OSFileSystem) MkdirAll(p string, mode os.FileMode) error { return os.MkdirAll(p, mode) }
func (OSFileSystem) CreateTemp(dir, pattern string) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}
func (OSFileSystem) Remove(p string) error                 { return os.Remove(p) }
func (OSFileSystem) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (OSFileSystem) Locked(p string, wait time.Duration, fn func() error) error {
	return fsio.Locked(p, wait, fn)
}
func (OSFileSystem) WriteFileAtomic(p string, b []byte) error { return fsio.WriteFileAtomic(p, b) }
