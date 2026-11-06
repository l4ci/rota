package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// workerInbox is `rota worker inbox <slot>`: a worker reads the relays the
// orchestrator wrote to its durable inbox. The pane copy of a relay can cross
// the worker's ROTA-DONE; this one waits, and `worker done` refuses while any
// of it is unread or actionable requests remain pending. --ack changes read
// state only; --handled/--returned explicitly resolve requests under the lock.
func workerInbox(fs *flag.FlagSet) RunFunc {
	ack := fs.Bool("ack", false, "mark the listed mail read")
	handled := fs.Int("handled", 0, "mark a request handled (requires --reason)")
	returned := fs.Int("returned", 0, "return a request to the orchestrator (requires --reason)")
	reason := fs.String("reason", "", "explain the disposition")
	all := fs.Bool("all", false, "include all inbox history")
	upto := fs.Int("upto", 0, "with --ack: mark only mail up to this sequence number (the highest one listed)")
	return func(c *Ctx, args []string) (Result, error) {
		flags := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { flags[f.Name] = true })
		resolving := flags["handled"] || flags["returned"]
		if (flags["handled"] && flags["returned"]) || (resolving && (*ack || *all || flags["upto"])) {
			return Result{}, Usage("--handled and --returned must be used alone with --reason")
		}
		if resolving && ((*handled <= 0 && flags["handled"]) || (*returned <= 0 && flags["returned"]) || strings.TrimSpace(*reason) == "") {
			return Result{}, Usage("a positive mail sequence and nonblank --reason are required")
		}
		if flags["reason"] && !resolving {
			return Result{}, Usage("--reason needs --handled or --returned")
		}
		if *all && *ack {
			return Result{}, Usage("--all cannot be combined with --ack")
		}
		if *upto != 0 && !*ack {
			return Result{}, Usage("--upto needs --ack")
		}
		if *upto < 0 {
			return Result{}, Usage("--upto must be a sequence number, got %d", *upto)
		}
		slotName, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		poolRoot, err := returnRoot(c, slotName)
		if err != nil {
			return Result{}, err
		}
		var mail []worker.Mail
		unreadAfter, pendingAfter := 0, 0
		var dispositionErr error
		found, err := worker.UpdateSlot(poolRoot, slotName, func(s *worker.Slot) {
			if resolving {
				seq, disposition := *handled, "handled"
				if flags["returned"] {
					seq, disposition = *returned, "returned"
				}
				var m worker.Mail
				m, dispositionErr = s.ResolveMail(seq, disposition, *reason)
				if dispositionErr == nil {
					mail = []worker.Mail{m}
				}
			} else if *all {
				mail = s.Inbox()
			} else if *ack {
				mail = s.AckUnread(*upto)
			} else {
				mail = s.Waiting()
			}
			unreadAfter, pendingAfter = len(s.Unread()), len(s.Pending())
		})
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{}, Resolution("slot '%s' is not in the pool", slotName)
		}
		if dispositionErr != nil {
			return Result{}, dispositionErr
		}
		items := make([]any, len(mail))
		for i, m := range mail {
			items[i] = m.Object()
		}
		d := jsonx.NewObject()
		d.Set("slot", slotName)
		d.Set("mail", items)
		d.Set("unread", unreadAfter)
		d.Set("pending", pendingAfter)
		d.Set("acked", ackedCount(*ack, len(mail)))
		d.Set("changed", (*ack || resolving) && len(mail) > 0)
		if len(mail) == 0 {
			return Result{Data: d, Text: "no unread mail"}, nil
		}
		var sb strings.Builder
		for i, m := range mail {
			if i > 0 {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "mail %d (%s, orchestrator relay round %d, %s):\n%s\n", m.Seq, m.Kind, m.Round, m.At, strings.TrimRight(m.Text, "\n"))
			if m.Actionable {
				fmt.Fprintf(&sb, "actionable: %s; task %s; attempt %s; read %t\n", m.Disposition, m.Task, m.Attempt, m.Read)
				if m.Reason != "" {
					fmt.Fprintf(&sb, "reason: %s\n", m.Reason)
				}
			} else {
				sb.WriteString("informational (legacy acknowledgement applies)\n")
			}
		}
		if *ack {
			fmt.Fprintf(&sb, "\nacknowledged %d mail(s)\n", len(mail))
		} else if !resolving && !*all {
			fmt.Fprintf(&sb, "\n%d unread, %d pending: mark read with `rota worker inbox %s --ack --upto %d`; resolve requests with --handled <seq> or --returned <seq> and --reason <text>\n", unreadAfter, pendingAfter, slotName, mail[len(mail)-1].Seq)
		}
		return Result{Data: d, Text: strings.TrimRight(sb.String(), "\n")}, nil
	}
}

func ackedCount(ack bool, n int) int {
	if ack {
		return n
	}
	return 0
}
