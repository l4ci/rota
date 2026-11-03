package worker

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

// QuotaAccount identifies a configured login home by harness and account name.
type QuotaAccount struct{ Kind, Name string }

// Quotas collects display-only account capacity, independently of scheduling meters.
// Read replaces the process boundary in tests and must honor its context.
type Quotas struct {
	Env     Env
	Options harness.QuotaOptions
	Read    func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot
}

// List reports each available reporting facet independently of worker defaults.
// A facet without named homes reports its explicitly caller-current ambient home.
func (q *Quotas) List(ctx context.Context, root string) []harness.QuotaSnapshot {
	e := q.Env.WithDefaults()
	cfg := e.Config(root)
	home, _ := e.Home()
	refs := []QuotaAccount{}
	for _, kind := range harness.Kinds() {
		if _, ok := harness.LookupQuotaReporter(kind); !ok {
			continue
		}
		hz, ok := harness.LookupAccounts(kind)
		if !ok {
			continue
		}
		entries := hz.Accounts(harness.ConfigFor(cfg, kind)).In(home).Entries
		for _, entry := range entries {
			refs = append(refs, QuotaAccount{kind, entry.Name})
		}
		if len(entries) == 0 {
			refs = append(refs, QuotaAccount{kind, ""})
		}
	}
	return q.collect(ctx, e, cfg, home, refs)
}

// ForAccounts resolves historical names only against unique current configured
// homes. An unnamed historical account can only report caller-current ambient
// capacity when there are no configured homes; it cannot establish past identity.
func (q *Quotas) ForAccounts(ctx context.Context, root string, refs []QuotaAccount) []harness.QuotaSnapshot {
	e := q.Env.WithDefaults()
	home, _ := e.Home()
	return q.collect(ctx, e, e.Config(root), home, refs)
}

type quotaJob struct {
	index    int
	kind     string
	reporter harness.QuotaReporter
	req      harness.QuotaRequest
}

func (q *Quotas) collect(ctx context.Context, e Env, cfg any, home string, refs []QuotaAccount) []harness.QuotaSnapshot {
	opts := q.Options
	if opts.Now == nil {
		opts.Now = e.Now
	}
	if opts.UserHome == "" {
		opts.UserHome = home
	}
	rows := []harness.QuotaSnapshot{}
	jobs := []quotaJob{}
	seen := map[QuotaAccount]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		reporter, ok := harness.LookupQuotaReporter(ref.Kind)
		if !ok {
			continue
		}
		hz, ok := harness.LookupAccounts(ref.Kind)
		if !ok {
			continue
		}
		entries := hz.Accounts(harness.ConfigFor(cfg, ref.Kind)).In(home).Entries
		req := harness.QuotaRequest{Name: ref.Name}
		reason := ""
		if ref.Name == "" {
			if len(entries) != 0 {
				reason = "historical ambient account cannot resolve with named accounts"
			} else {
				req.Name = "ambient/current"
				req.Ambient = true
				req.Account = hz.AmbientAccount(e.Getenv)
				req.Account.Home = harness.AccountList{Entries: []harness.AccountEntry{{Dir: req.Account.Home}}}.In(home).Entries[0].Dir
			}
		} else {
			matches := 0
			for _, entry := range entries {
				if entry.Name == ref.Name {
					matches++
					req.Account = harness.Account{Home: entry.Dir}
				}
			}
			switch {
			case matches == 0:
				reason = "configured account missing"
			case matches > 1:
				reason = "configured account name is ambiguous"
				req.Account = harness.Account{}
			case req.Account.Home == "":
				reason = "named account home missing"
			case !filepath.IsAbs(req.Account.Home):
				reason = "account home must be absolute"
			}
		}
		row := harness.QuotaSnapshot{Kind: ref.Kind, Name: req.Name, Home: req.Account.Home, Ambient: req.Ambient, Source: "not-read", IdentityStatus: "unavailable", ObservedAt: opts.Now().UTC(), Status: "unavailable", Reason: reason, Windows: []harness.QuotaWindow{}}
		index := len(rows)
		rows = append(rows, row)
		if reason == "" {
			jobs = append(jobs, quotaJob{index, ref.Kind, reporter, req})
		}
	}
	if len(jobs) == 0 {
		return rows
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	var readers sync.WaitGroup
	defer func() { cancel(); readers.Wait() }()
	type result struct {
		index    int
		snapshot harness.QuotaSnapshot
	}
	results := make(chan result, len(jobs))
	pending := make(chan quotaJob, len(jobs))
	for _, job := range jobs {
		pending <- job
	}
	close(pending)
	read := q.Read
	if read == nil {
		read = func(ctx context.Context, r harness.QuotaReporter, req harness.QuotaRequest, opts harness.QuotaOptions) harness.QuotaSnapshot {
			return r.ReadQuota(ctx, req, opts)
		}
	}
	for i := 0; i < 2 && i < len(jobs); i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for job := range pending {
				if ctx.Err() != nil {
					return
				}
				snapshot := read(ctx, job.reporter, job.req, opts)
				snapshot.Kind = job.kind
				snapshot.Name = job.req.Name
				snapshot.Ambient = job.req.Ambient
				if !job.req.Ambient {
					snapshot.Home = job.req.Account.Home
				}
				if snapshot.ObservedAt.IsZero() {
					snapshot.ObservedAt = opts.Now().UTC()
				}
				results <- result{job.index, snapshot}
			}
		}()
	}
	remaining := map[int]bool{}
	for _, job := range jobs {
		remaining[job.index] = true
	}
	for len(remaining) > 0 {
		select {
		case result := <-results:
			if ctx.Err() == nil {
				rows[result.index] = result.snapshot
				delete(remaining, result.index)
			}
		case <-ctx.Done():
			for index := range remaining {
				rows[index].Reason = "quota report timed out or canceled"
				rows[index].ObservedAt = opts.Now().UTC()
			}
			return rows
		}
	}
	return rows
}
