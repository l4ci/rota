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
// of it is unread. Listing leaves the mail unread; --ack marks what it listed
// read, in the same registry update, so mail that arrives meanwhile stays.
func workerInbox(fs *flag.FlagSet) RunFunc {
	ack := fs.Bool("ack", false, "mark the listed mail read")
	upto := fs.Int("upto", 0, "with --ack: mark only mail up to this sequence number (the highest one listed)")
	return func(c *Ctx, args []string) (Result, error) {
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
		unreadAfter := 0
		found, err := worker.UpdateSlot(poolRoot, slotName, func(s *worker.Slot) {
			if *ack {
				mail = s.AckUnread(*upto)
			} else {
				mail = s.Unread()
			}
			unreadAfter = len(s.Unread())
		})
		if err != nil {
			return Result{}, err
		}
		if !found {
			return Result{}, Resolution("slot '%s' is not in the pool", slotName)
		}
		items := make([]any, len(mail))
		for i, m := range mail {
			items[i] = m.Object()
		}
		d := jsonx.NewObject()
		d.Set("slot", slotName)
		d.Set("mail", items)
		d.Set("unread", unreadAfter)
		d.Set("acked", ackedCount(*ack, len(mail)))
		d.Set("changed", *ack && len(mail) > 0)
		if len(mail) == 0 {
			return Result{Data: d, Text: "no unread mail"}, nil
		}
		var sb strings.Builder
		for i, m := range mail {
			if i > 0 {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "mail %d (%s, orchestrator relay round %d, %s):\n%s\n", m.Seq, m.Kind, m.Round, m.At, strings.TrimRight(m.Text, "\n"))
		}
		if *ack {
			fmt.Fprintf(&sb, "\nacknowledged %d mail(s)\n", len(mail))
		} else {
			fmt.Fprintf(&sb, "\n%d unread: once you have acted on it, run `rota worker inbox %s --ack --upto %d`\n", len(mail), slotName, mail[len(mail)-1].Seq)
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
