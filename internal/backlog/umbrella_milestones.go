package backlog

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// Milestones at an umbrella root (hvlib_backend.UmbrellaIssueBackend): the
// tracking issue, the milestone plan and its slice plans live on the home
// sub-repo, and every sub-repo that uses a milestone has its own native
// milestone "MNN — <title>". IDs are minted over the native milestones of all
// sub-repos, and a milestone is shipped only when every native one is closed.

// hasMilestone reports whether native milestone title starts with mid and the
// ID does not run into a word character (M05 does not match M050).
func hasMilestone(title, mid string) bool {
	t := pystr.Strip(title)
	if !strings.HasPrefix(t, mid) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(t[len(mid):])
	return t[len(mid):] == "" || !pystr.IsWord(r)
}

// nativeSet is the native milestones of every sub-repo, fetched once.
type nativeSet map[string][]tracker.Milestone

func (u *Umbrella) fetchNatives() (nativeSet, error) {
	set := nativeSet{}
	err := u.eachRepo(func(name string, sub *Issues) error {
		mt, err := sub.milestoneTracker()
		if err != nil {
			return err
		}
		found, err := mt.Milestones(u.ctx(), "all")
		set[name] = found
		return err
	})
	return set, err
}

// of is the native milestone of mid per sub-repo, an open one before a closed
// one; sub-repos without it are absent.
func (s nativeSet) of(mid string) map[string]tracker.Milestone {
	out := map[string]tracker.Milestone{}
	for name, found := range s {
		var hits []tracker.Milestone
		for _, m := range found {
			if hasMilestone(m.Title, mid) {
				hits = append(hits, m)
			}
		}
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].State != "closed" && hits[j].State == "closed" })
		if len(hits) > 0 {
			out[name] = hits[0]
		}
	}
	return out
}

// NextMilestoneID is MNN with NN one above the highest milestone ID over the
// native milestones of every sub-repo.
func (u *Umbrella) NextMilestoneID() (string, error) {
	set, err := u.fetchNatives()
	if err != nil {
		return "", err
	}
	highest := 0
	for _, found := range set {
		for _, nm := range found {
			if id, _, ok := parseMSTitle(nm.Title); ok {
				if n, err := Atoi(id[1:]); err == nil {
					highest = max(highest, n)
				}
			}
		}
	}
	return fmt.Sprintf("M%02d", highest+1), nil
}

// MilestoneAdd creates the milestone on the home sub-repo with an ID minted
// over all sub-repos (mid "" mints).
func (u *Umbrella) MilestoneAdd(mid, title, summary string, depends []string, today string) (string, error) {
	return viaHome(u, func(home *Issues) (string, error) {
		if mid == "" {
			var err error
			if mid, err = u.NextMilestoneID(); err != nil {
				return "", err
			}
		}
		return home.MilestoneAdd(mid, title, summary, depends, today)
	})
}

// MilestoneList is the home sub-repo's list, except that a shipped milestone
// is active while any sub-repo's native milestone is still open; ready
// follows the adjusted statuses.
func (u *Umbrella) MilestoneList() ([]ms.Entry, error) {
	home, err := u.homeSub()
	if err != nil {
		return nil, err
	}
	rows, err := home.MilestoneList()
	if err != nil {
		return nil, err
	}
	var set nativeSet
	for i := range rows {
		if rows[i].Status != "shipped" {
			continue
		}
		if set == nil {
			if set, err = u.fetchNatives(); err != nil {
				return nil, err
			}
		}
		for _, m := range set.of(rows[i].ID) {
			if m.State != "closed" {
				rows[i].Status = "active"
				break
			}
		}
	}
	shipped := map[string]bool{}
	for _, r := range rows {
		if r.Status == "shipped" {
			shipped[r.ID] = true
		}
	}
	for i := range rows {
		rows[i].Ready = true
		for _, d := range rows[i].Depends {
			if !shipped[d] {
				rows[i].Ready = false
			}
		}
	}
	return rows, nil
}

// MilestoneShow is the milestone plan on the home sub-repo.
func (u *Umbrella) MilestoneShow(mid string) (string, error) {
	return viaHome(u, func(home *Issues) (string, error) { return home.MilestoneShow(mid) })
}

// MilestonePut replaces the milestone plan on the home sub-repo.
func (u *Umbrella) MilestonePut(mid, text string) error {
	_, err := viaHome(u, func(home *Issues) (struct{}, error) { return struct{}{}, home.MilestonePut(mid, text) })
	return err
}

// MilestoneStatus moves the milestone on the home sub-repo, then opens or
// closes the native milestone in every other sub-repo that has one.
func (u *Umbrella) MilestoneStatus(mid, status string) error {
	home, err := u.homeSub()
	if err != nil {
		return err
	}
	if err := home.MilestoneStatus(mid, status); err != nil {
		return err
	}
	want := "open"
	if status == "shipped" || status == "archived" {
		want = "closed"
	}
	set, err := u.fetchNatives()
	if err != nil {
		return err
	}
	for name, m := range set.of(mid) {
		if name == home.Repo || m.State == want {
			continue
		}
		sub, err := u.sub(name)
		if err != nil {
			return err
		}
		mt, err := sub.milestoneTracker()
		if err != nil {
			return err
		}
		if err := mt.EditMilestone(u.ctx(), m.Number, tracker.MilestoneEdit{State: &want}); err != nil {
			return err
		}
	}
	return nil
}
