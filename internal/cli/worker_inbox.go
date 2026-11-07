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
		if flags["handled"] && flags["returned"] {
			return Result{}, Usage("--handled and --returned cannot be combined: resolve one request per call")
		}
		if resolving {
			verb := "--handled"
			if flags["returned"] {
				verb = "--returned"
			}
			for _, other := range []string{"ack", "all", "upto"} {
				if flags[other] {
					return Result{}, Usage("%s cannot be combined with --%s", verb, other)
				}
			}
			if (*handled <= 0 && flags["handled"]) || (*returned <= 0 && flags["returned"]) {
				return Result{}, Usage("%s needs a positive mail sequence number", verb)
			}
			if strings.TrimSpace(*reason) == "" {
				return Result{}, Usage("%s needs a nonblank --reason", verb)
			}
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
		next := ""
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
			next = s.InboxNext()
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
			text := "no unread mail"
			if *all {
				text = "no mail"
			}
			if next != "" {
				text += "; still to do: " + next
			}
			return Result{Data: d, Text: text}, nil
		}
		var sb strings.Builder
		for i, m := range mail {
			if i > 0 {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "mail %d (%s, orchestrator relay round %d, %s):\n%s\n", m.Seq, m.Kind, m.Round, m.At, strings.TrimRight(m.Text, "\n"))
			sb.WriteString(mailStatus(m) + "\n")
		}
		switch {
		case resolving:
			fmt.Fprintf(&sb, "\nmail %d marked %s\n", mail[0].Seq, mail[0].Disposition)
		case *ack:
			fmt.Fprintf(&sb, "\nacknowledged %d mail(s)\n", len(mail))
		}
		if next != "" && !*all {
			fmt.Fprintf(&sb, "\nstill to do: %s\n", next)
		}
		return Result{Data: d, Text: strings.TrimRight(sb.String(), "\n")}, nil
	}
}

// mailStatus is the line under a listed mail saying what it still needs.
func mailStatus(m worker.Mail) string {
	if !m.Actionable {
		return "informational: --ack is enough"
	}
	parts := []string{"request " + m.Disposition}
	if m.Task != "" {
		parts = append(parts, "task "+m.Task)
	}
	if m.Attempt != "" {
		parts = append(parts, "attempt "+m.Attempt)
	}
	if m.Read {
		parts = append(parts, "read")
	} else {
		parts = append(parts, "unread")
	}
	line := strings.Join(parts, "; ")
	if m.Reason != "" {
		line += "\nreason: " + m.Reason
	}
	return line
}

func ackedCount(ack bool, n int) int {
	if ack {
		return n
	}
	return 0
}
