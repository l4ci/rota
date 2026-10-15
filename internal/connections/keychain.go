package connections

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/l4ci/rota/internal/proc"
)

const keychainService = "rota"

// OSKeychain is the keychain of goos: `security` on darwin, libsecret's
// `secret-tool` on linux. Nil on any other OS. Every call goes through run,
// so tests never reach a real keychain.
func OSKeychain(run proc.Runner, goos string) Store {
	switch goos {
	case "darwin", "linux":
		return osKeychain{run: run, goos: goos}
	}
	return nil
}

type osKeychain struct {
	run  proc.Runner
	goos string
}

func (osKeychain) Name() string { return StoreKeychain }

func (k osKeychain) call(ctx context.Context, stdin string, args ...string) (proc.Result, error) {
	c := proc.Cmd{Name: "security", Args: args}
	if k.goos == "linux" {
		c.Name = "secret-tool"
		if stdin != "" {
			c.Stdin = []byte(stdin)
		}
	}
	r, err := k.run(ctx, c)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return r, fmt.Errorf("%w: %s not found", ErrUnavailable, c.Name)
		}
		return r, err
	}
	return r, nil
}

func (k osKeychain) Get(ctx context.Context, name string) (string, bool, error) {
	if k.goos == "darwin" {
		r, err := k.call(ctx, "", "find-generic-password", "-s", keychainService, "-a", name, "-w")
		switch {
		case err != nil:
			return "", false, err
		case r.ExitCode == 0:
			return strings.TrimRight(r.Stdout, "\r\n"), true, nil
		case r.ExitCode == 44: // errSecItemNotFound
			return "", false, nil
		}
		return "", false, fmt.Errorf("%w: security exit %d: %s", ErrUnavailable, r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	r, err := k.call(ctx, "", "lookup", "service", keychainService, "account", name)
	switch {
	case err != nil:
		return "", false, err
	case r.ExitCode == 0 && r.Stdout != "":
		return strings.TrimRight(r.Stdout, "\r\n"), true, nil
	case r.ExitCode == 0 || strings.TrimSpace(r.Stderr) == "":
		return "", false, nil // secret-tool exits 1 with no output for a missing item
	}
	return "", false, fmt.Errorf("%w: %s", ErrUnavailable, strings.TrimSpace(r.Stderr))
}

func (k osKeychain) Set(ctx context.Context, name, secret string) error {
	var r proc.Result
	var err error
	if k.goos == "darwin" {
		// security takes the secret as an argument: visible in ps for the
		// instant the call runs. It has no stdin form for -w.
		r, err = k.call(ctx, "", "add-generic-password", "-U", "-s", keychainService, "-a", name, "-w", secret)
	} else {
		r, err = k.call(ctx, secret, "store", "--label=rota "+name, "service", keychainService, "account", name)
	}
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("%w: exit %d: %s", ErrUnavailable, r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return nil
}

func (k osKeychain) Delete(ctx context.Context, name string) error {
	var r proc.Result
	var err error
	if k.goos == "darwin" {
		r, err = k.call(ctx, "", "delete-generic-password", "-s", keychainService, "-a", name)
		if err == nil && r.ExitCode == 44 {
			return nil
		}
	} else {
		r, err = k.call(ctx, "", "clear", "service", keychainService, "account", name)
		if err == nil && r.ExitCode != 0 && strings.TrimSpace(r.Stderr) == "" {
			return nil
		}
	}
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("%w: exit %d: %s", ErrUnavailable, r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return nil
}
