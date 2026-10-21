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
	}
	if stdin != "" {
		c.Stdin = []byte(stdin)
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

// SecurityRead reads one macOS generic password through `security`, the one
// implementation of that read. account "" matches on service alone. ok is
// false when no item exists; ErrUnavailable wraps every other failure.
func SecurityRead(ctx context.Context, run proc.Runner, service, account string) (secret string, ok bool, err error) {
	args := []string{"find-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	r, err := run(ctx, proc.Cmd{Name: "security", Args: append(args, "-w")})
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", false, fmt.Errorf("%w: security not found", ErrUnavailable)
	case err != nil:
		return "", false, err
	case r.ExitCode == 0:
		return strings.TrimRight(r.Stdout, "\r\n"), true, nil
	case r.ExitCode == 44: // errSecItemNotFound
		return "", false, nil
	}
	return "", false, fmt.Errorf("%w: security exit %d: %s", ErrUnavailable, r.ExitCode, strings.TrimSpace(r.Stderr))
}

func (k osKeychain) Get(ctx context.Context, name string) (string, bool, error) {
	if k.goos == "darwin" {
		return SecurityRead(ctx, k.run, keychainService, name)
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
		// `security -i` reads its commands from stdin, so argv never holds the
		// secret. The call's exit status is not trusted in interactive mode:
		// read the item back instead.
		cmd := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", securityQuote(keychainService), securityQuote(name), securityQuote(secret))
		r, err = k.call(ctx, cmd, "-i")
		if err != nil {
			return err
		}
		got, ok, gerr := k.Get(ctx, name)
		if gerr != nil || !ok || got != secret {
			return fmt.Errorf("%w: security -i did not store the item: %s", ErrUnavailable, strings.TrimSpace(r.Stderr))
		}
		return nil
	}
	r, err = k.call(ctx, secret, "store", "--label=rota "+name, "service", keychainService, "account", name)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("%w: exit %d: %s", ErrUnavailable, r.ExitCode, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// securityQuote double-quotes s for `security -i`, escaping \ and ".
func securityQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
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
