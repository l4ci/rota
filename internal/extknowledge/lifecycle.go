package extknowledge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/l4ci/rota/internal/fsio"
)

// withCheckout owns the checkout across processes, Clone values, state dirs
// and path aliases. The lock lives in this checkout's Git directory so it
// neither dirties the tree nor depends on a project's rota configuration.
// Refuse contention before running fn; the kernel releases ownership even
// if the process dies, and the persistent sidecar keeps waiters on one inode.
func (c Clone) withCheckout(ctx context.Context, fn func() error) error {
	dir, err := c.out(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	err = fsio.Locked(filepath.Join(dir, "rota-knowledge"), 0, fn)
	if errors.Is(err, fsio.ErrLockTimeout) {
		return fmt.Errorf("%w: retry after the active operation finishes in %s", ErrBusy, c.Dir)
	}
	return err
}

// mutate owns the complete read/modify/land lifecycle, including sync's
// cleanliness check and fast-forward, input reads and policy, and land's
// deferred rollback and branch restoration. No caller may plan before this.
func (c Clone) mutate(ctx context.Context, fn func() error) error {
	return c.withCheckout(ctx, func() error {
		if err := c.syncForWrite(ctx); err != nil {
			return err
		}
		return fn()
	})
}
