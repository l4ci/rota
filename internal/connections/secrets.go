package connections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Store names, as `rota connect list` reports them.
const (
	StoreKeychain = "keychain"
	StoreFile     = "file"
	StoreEnv      = "env"
)

// ErrUnavailable is a keychain that cannot be used on this machine: no tool,
// no desktop session. The caller falls back to the file store.
var ErrUnavailable = errors.New("keychain unavailable")

// Store holds secrets by connection name.
type Store interface {
	Name() string
	// Get is the secret, and whether there was one.
	Get(ctx context.Context, name string) (string, bool, error)
	Set(ctx context.Context, name, secret string) error
	// Delete removes the secret; a missing one is not an error.
	Delete(ctx context.Context, name string) error
}

// EnvVar is the variable that overrides the stored secret of a connection:
// ROTA_<NAME>_TOKEN, the name upper-cased with every non-alphanumeric as _.
func EnvVar(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "ROTA_" + b.String() + "_TOKEN"
}

// Secrets is the lookup order: the env override, the keychain, the file.
type Secrets struct {
	Getenv   func(string) string
	Keychain Store // nil: no keychain on this machine
	File     Store
}

// Saved says where Save put a secret. Note is set when the keychain was
// tried and failed, so the fallback is never silent.
type Saved struct{ Store, Note string }

// Save stores the secret in the keychain when it works, else in the file, and
// clears the other store so one name never holds two secrets.
func (s *Secrets) Save(ctx context.Context, name, secret string) (Saved, error) {
	var note string
	if s.Keychain != nil {
		err := s.Keychain.Set(ctx, name, secret)
		if err == nil {
			if derr := s.File.Delete(ctx, name); derr != nil {
				return Saved{}, derr
			}
			return Saved{Store: StoreKeychain}, nil
		}
		note = "keychain not used: " + err.Error()
		// Stored prefers the keychain: an old entry there would shadow the new
		// file secret. Remove it, or fail rather than leave it.
		_ = s.Keychain.Delete(ctx, name)
		if _, stale, _ := s.Keychain.Get(ctx, name); stale {
			return Saved{}, fmt.Errorf("keychain refused the new secret (%v) and still holds an old one for %q; remove it with your keychain tool, then retry", err, name)
		}
	} else {
		note = "no keychain on this machine"
	}
	if err := s.File.Set(ctx, name, secret); err != nil {
		return Saved{}, err
	}
	return Saved{Store: StoreFile, Note: note}, nil
}

// Stored is the store that holds name's secret, ignoring the env override:
// StoreKeychain, StoreFile, or "" for none. A keychain that errors is skipped
// when the file has the secret; with neither, its error is returned.
func (s *Secrets) Stored(ctx context.Context, name string) (secret, store string, err error) {
	var kerr error
	if s.Keychain != nil {
		v, ok, e := s.Keychain.Get(ctx, name)
		if ok {
			return v, StoreKeychain, nil
		}
		if e != nil && !errors.Is(e, ErrUnavailable) {
			kerr = e
		}
	}
	v, ok, ferr := s.File.Get(ctx, name)
	if ferr != nil {
		return "", "", ferr
	}
	if ok {
		return v, StoreFile, nil
	}
	return "", "", kerr
}

// Resolve is the secret a request uses: the env override first, else the
// stored one. store is "" when there is none.
func (s *Secrets) Resolve(ctx context.Context, name string) (secret, store string, err error) {
	if s.Getenv != nil {
		if v := strings.TrimSpace(s.Getenv(EnvVar(name))); v != "" {
			return v, StoreEnv, nil
		}
	}
	return s.Stored(ctx, name)
}

// Delete clears the secret from every store.
func (s *Secrets) Delete(ctx context.Context, name string) error {
	if s.Keychain != nil {
		if err := s.Keychain.Delete(ctx, name); err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}
	}
	return s.File.Delete(ctx, name)
}

// SecretsFile is the path of the file store under dir.
func SecretsFile(dir string) string { return filepath.Join(dir, "secrets.json") }

// FileStore keeps secrets in secrets.json under dir, mode 0600: the fallback
// where there is no keychain (headless Linux). dir is the data dir, not the
// config dir, which users sync into dotfiles repos. A secrets.json left in
// legacyDir (the old location; "" for none) is moved into dir on first use.
func FileStore(dir, legacyDir string) Store {
	f := fileStore{path: SecretsFile(dir)}
	if legacyDir != "" {
		f.legacy = SecretsFile(legacyDir)
	}
	return f
}

type fileStore struct{ path, legacy string }

// migrate moves the legacy file to path when path does not exist yet. The
// result is always mode 0600, whatever the legacy file had; across devices it
// is copied, then the original removed.
func (f fileStore) migrate() error {
	if f.legacy == "" || f.legacy == f.path {
		return nil
	}
	if _, err := os.Stat(f.path); err == nil {
		return nil
	}
	_, err := os.Stat(f.legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	if os.Rename(f.legacy, f.path) == nil {
		return os.Chmod(f.path, 0o600)
	}
	raw, err := os.ReadFile(f.legacy)
	if err != nil {
		return err
	}
	tf, err := os.CreateTemp(filepath.Dir(f.path), "secrets-*.tmp")
	if err != nil {
		return err
	}
	tmp := tf.Name()
	_, err = tf.Write(raw)
	if cerr := tf.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, f.path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(f.legacy)
}

func (fileStore) Name() string { return StoreFile }

func (f fileStore) Get(_ context.Context, name string) (string, bool, error) {
	if err := f.migrate(); err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	v, err := jsonx.Decode(raw)
	o, _ := v.(*jsonx.Object)
	if err != nil || o == nil {
		return "", false, fmt.Errorf("%s does not parse; fix or remove it", f.path)
	}
	s := jsonx.Str(o, name)
	return s, s != "", nil
}

func (f fileStore) Set(_ context.Context, name, secret string) error {
	return f.update(func(o *jsonx.Object) { o.Set(name, secret) })
}

func (f fileStore) Delete(_ context.Context, name string) error {
	if err := f.migrate(); err != nil {
		return err
	}
	if _, err := os.Stat(f.path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return f.update(func(o *jsonx.Object) { o.Delete(name) })
}

// update is a locked read-modify-write that writes a 0600 file atomically.
func (f fileStore) update(mutate func(*jsonx.Object)) error {
	if err := f.migrate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	return fsio.Locked(f.path, fsio.LockTimeout, func() error {
		doc := jsonx.NewObject()
		if raw, err := os.ReadFile(f.path); err == nil {
			v, derr := jsonx.Decode(raw)
			o, _ := v.(*jsonx.Object)
			if derr != nil || o == nil {
				return fmt.Errorf("%s does not parse; fix or remove it, rota will not overwrite it", f.path)
			}
			doc = o
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		mutate(doc)
		data, err := jsonx.Marshal(doc)
		if err != nil {
			return err
		}
		// CreateTemp creates exclusively, mode 0600, under a random name.
		tf, err := os.CreateTemp(filepath.Dir(f.path), "secrets-*.tmp")
		if err != nil {
			return err
		}
		tmp := tf.Name()
		_, err = tf.Write(data)
		if cerr := tf.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, f.path)
		}
		if err != nil {
			os.Remove(tmp)
		}
		return err
	})
}
