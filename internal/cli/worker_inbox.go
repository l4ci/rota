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
	return func(c *Ctx, args []string) (Result, error) {
		slotName, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		poolRoot, err := returnRoot(c, slotName)
		if err != nil {
			return Result{}, err
		}
		var mail []worker.Mail
		found, err := worker.UpdateSlot(poolRoot, slotName, func(s *worker.Slot) {
			if *ack {
				mail = s.AckUnread()
				return
			}
			mail = s.Unread()
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
		d.Set("unread", len(mail)-ackedCount(*ack, len(mail)))
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
			fmt.Fprintf(&sb, "\n%d unread: run `rota worker inbox %s --ack` once you have acted on it\n", len(mail), slotName)
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
