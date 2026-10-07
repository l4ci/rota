package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

func roundTransfer(fs *flag.FlagSet) RunFunc {
	to := fs.String("to", "", "a roster slot, or human")
	note := fs.String("note-file", "", "what is done and what is next (- for stdin)")
	body := fs.String("body-file", "", "decisions already settled, passed verbatim to the receiver (- for stdin)")
	tier := fs.String("tier", "", "receiver's tier: light, standard or heavy (default round.tier)")
	tierReason := fs.String("tier-reason", "", "one line on why; required above the default tier")
	accept := fs.Bool("accept-overlap", false, "skip the file-overlap check only")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round transfer takes one item ID")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if *to == "" {
			return Result{}, Usage("--to is required: a roster slot or human")
		}
		text, err := roundNote(c, "note-file", *note)
		if err != nil {
			return Result{}, err
		}
		bf := *body
		if bf == "-" {
			f, err := os.CreateTemp("", "rota-round-decisions-")
			if err != nil {
				return Result{}, err
			}
			defer os.Remove(f.Name())
			if _, err := f.ReadFrom(c.Stdin); err != nil {
				return Result{}, err
			}
			f.Close()
			bf = f.Name()
		}
		env, be, err := moveEnv(c, root)
		if err != nil {
			return Result{}, err
		}
		id, _, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		res, err := env.Transfer(c.Context(), root, be, round.TransferOpts{
			Issue: id, To: *to, Note: text, BodyFile: bf, AcceptOverlap: *accept,
			Tier: *tier, TierReason: *tierReason, HolderPID: *pid, Settings: set,
		})
		if err != nil {
			r, ferr := moveFailure(err, res.Changed)
			if res.Changed && r.Data != nil { // the move happened before the dispatch failed
				r.Data = transferData(res)
			}
			return r, ferr
		}
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		return Result{Data: transferData(res), Text: fmt.Sprintf("transferred %s from %s to %s on %s", res.Issue, res.From, res.To, res.Branch)}, nil
	}
}

func transferData(res round.Transferred) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("issue", res.Issue)
	d.Set("from", res.From)
	d.Set("to", res.To)
	d.Set("branch", res.Branch)
	d.Set("head", sha7(res.Head))
	d.Set("salvaged", res.Salvaged)
	setIf(d, "claimId", res.ClaimID)
	if res.Host != "" { // solo: the brief comes back instead of going to a pane
		d.Set("host", res.Host)
		d.Set("brief", res.Brief)
		d.Set("worktree", res.Worktree)
	}
	d.Set("dispatched", res.Dispatched)
	d.Set("changed", res.Changed)
	return d
}
