package backlog

import (
	"context"
	"errors"
)

// The routing layer: how an Umbrella reaches the Issues backend of a sub-repo.
// A delegated verb states only what to call on the owning sub-repo; resolving
// the owner (qualified or bare reference, ambiguity), the per-repo scope and
// the home sub-repo live here once.

// onOwner runs call on the sub-repo that owns ref, with the reference as that
// sub-repo spells it. An unknown reference is ErrNotFound.
func (u *Umbrella) onOwner(ref string, call func(s *Issues, plain string) error) error {
	s, plain, err := u.owner(ref)
	if err != nil {
		return err
	}
	return call(s, plain)
}

// viaOwner is onOwner for a verb that returns one value.
func viaOwner[T any](u *Umbrella, ref string, call func(s *Issues, plain string) (T, error)) (T, error) {
	var out T
	err := u.onOwner(ref, func(s *Issues, plain string) (err error) {
		out, err = call(s, plain)
		return err
	})
	return out, err
}

// viaRepo is the per-repo twin: call runs on the --repo sub-repo (perRepo).
func viaRepo[T any](u *Umbrella, call func(name string, s *Issues) (T, error)) (T, error) {
	name, s, err := u.perRepo()
	if err != nil {
		var zero T
		return zero, err
	}
	return call(name, s)
}

// viaHome is the twin for verbs that live on the home sub-repo.
func viaHome[T any](u *Umbrella, call func(home *Issues) (T, error)) (T, error) {
	home, err := u.homeSub()
	if err != nil {
		var zero T
		return zero, err
	}
	return call(home)
}

// eachScoped runs call on every sub-repo reads cover, in registry order, and
// stops at the first error.
func (u *Umbrella) eachScoped(call func(name string, s *Issues) error) error {
	return u.each(u.scoped(), call)
}

// eachRepo is eachScoped over every registered sub-repo, whatever the scope.
func (u *Umbrella) eachRepo(call func(name string, s *Issues) error) error {
	names := make([]string, len(u.Repos))
	for i, r := range u.Repos {
		names[i] = r.Name
	}
	return u.each(names, call)
}

func (u *Umbrella) each(names []string, call func(name string, s *Issues) error) error {
	for _, name := range names {
		s, err := u.sub(name)
		if err != nil {
			return err
		}
		if err := call(name, s); err != nil {
			return err
		}
	}
	return nil
}

// qualifyID spells a sub-repo's item ID the umbrella way, "<repo>:<id>".
func qualifyID(repo, id string) string { return repo + ":" + id }

// Tracker capabilities beyond Tracker. A tracker declares one by implementing
// its interface; capability is the single place the backend looks that up and
// words the refusal.

// MilestoneCreator is the part of tracker.Adapter that creates a native milestone.
type MilestoneCreator interface {
	CreateMilestone(ctx context.Context, title, description string) (int, error)
}

const (
	noPRSupport        = "this tracker has no pull request support"
	noMilestoneSupport = "this tracker has no native milestone support"
)

// capability is the tracker of b as C, or an error saying what is missing.
func capability[C any](b *Issues, missing string) (C, error) {
	var zero C
	tr, err := b.tracker()
	if err != nil {
		return zero, err
	}
	c, ok := tr.(C)
	if !ok {
		return zero, errors.New(missing)
	}
	return c, nil
}
