package worker

import (
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
)

// MailReview is the kind of a REVIEW relay (`round review-relay`).
const MailReview = "review"

// Mail is one relay in a slot's durable inbox: the orchestrator writes it, the
// worker reads it with `worker inbox` and `--ack` marks it read. The pane copy
// can cross a ROTA-DONE; this one cannot, because `worker done` refuses while
// any mail is unread.
type Mail struct {
	Seq   int
	Round int
	At    string
	Kind  string
	Text  string
	Read  bool
}

// Object is the registry form of a mail, as `worker inbox` shows it.
func (m Mail) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("seq", m.Seq)
	o.Set("round", m.Round)
	o.Set("ts", m.At)
	o.Set("kind", m.Kind)
	o.Set("text", m.Text)
	return o
}

func (m Mail) raw() *jsonx.Object {
	o := m.Object()
	if m.Read {
		o.Set("read", true)
	}
	return o
}

func mailFrom(o *jsonx.Object) Mail {
	seq, _ := o.Get("seq")
	round, _ := o.Get("round")
	m := Mail{At: jsonx.Str(o, "ts"), Kind: jsonx.Str(o, "kind"), Text: jsonx.Str(o, "text"), Read: jsonx.Bool(o, "read")}
	m.Seq, _ = jsonx.Int(seq)
	m.Round, _ = jsonx.Int(round)
	return m
}

// Inbox is every mail the slot holds, oldest first.
func (s *Slot) Inbox() []Mail {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	var out []Mail
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok {
			out = append(out, mailFrom(o))
		}
	}
	return out
}

// Unread is the mail the worker has not acknowledged.
func (s *Slot) Unread() []Mail {
	var out []Mail
	for _, m := range s.Inbox() {
		if !m.Read {
			out = append(out, m)
		}
	}
	return out
}

// AckUnread marks every unread mail read and returns what it marked. Called
// inside one registry update with the read, so mail posted afterwards stays
// unread.
func (s *Slot) AckUnread() []Mail {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	var acked []Mail
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok && !jsonx.Bool(o, "read") {
			acked = append(acked, mailFrom(o))
			o.Set("read", true)
		}
	}
	return acked
}

func (s *Slot) postMail(m Mail) Mail {
	next := 1
	for _, e := range s.Inbox() {
		next = max(next, e.Seq+1)
	}
	m.Seq = next
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	s.o.Set("inbox", append(l, m.raw()))
	return m
}

func (s *Slot) retractMail(seq int) {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	keep := make([]any, 0, len(l))
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok && mailFrom(o).Seq == seq {
			continue
		}
		keep = append(keep, e)
	}
	if len(keep) == 0 {
		s.o.Delete("inbox")
		return
	}
	s.o.Set("inbox", keep)
}

// PostMail appends an unread mail to the slot's inbox under the registry lock.
// The round is the one the relay is signed with, so the worker can cite it.
func PostMail(root, slot, kind, text string, now time.Time) (Mail, error) {
	m := Mail{Round: roundOf(root), At: stamp.Format(now), Kind: kind, Text: text}
	var posted Mail
	found, err := UpdateSlot(root, slot, func(s *Slot) { posted = s.postMail(m) })
	if err == nil && !found {
		return Mail{}, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	return posted, err
}

// RetractMail removes one mail by sequence number: a relay whose dispatch
// failed was never sent and must not hold `worker done`.
func RetractMail(root, slot string, seq int) error {
	_, err := UpdateSlot(root, slot, func(s *Slot) { s.retractMail(seq) })
	return err
}
