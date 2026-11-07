package worker

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
)

// MailReview is the kind of a REVIEW relay (`round review-relay`).
const MailReview = "review"

// MailBase is the kind of a relay telling the worker its PR's base moved
// (`round reconcile --apply` retargeting a re-tagged issue).
const MailBase = "base"

// Mail is one relay in a slot's durable inbox: the orchestrator writes it, the
// worker reads it with `worker inbox` and `--ack` marks it read. New REVIEW
// and BASE requests also require an explicit handled/returned disposition.
// Legacy records retain acknowledgement-only semantics.
type Mail struct {
	Seq                                int
	Round                              int
	At                                 string
	Kind                               string
	Text                               string
	Read                               bool
	Actionable                         bool
	Task, Attempt, Disposition, Reason string
}

// Object is the registry form of a mail, as `worker inbox` shows it.
func (m Mail) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("seq", m.Seq)
	o.Set("round", m.Round)
	o.Set("ts", m.At)
	o.Set("kind", m.Kind)
	o.Set("text", m.Text)
	o.Set("read", m.Read)
	if m.Actionable {
		o.Set("actionable", true)
		o.Set("disposition", m.Disposition)
		for _, kv := range [][2]string{{"task", m.Task}, {"attempt", m.Attempt}, {"reason", m.Reason}} {
			if kv[1] != "" {
				o.Set(kv[0], kv[1])
			}
		}
	}
	return o
}

func (m Mail) raw() *jsonx.Object { return m.Object() }

func mailFrom(o *jsonx.Object) Mail {
	seq, _ := o.Get("seq")
	round, _ := o.Get("round")
	m := Mail{At: jsonx.Str(o, "ts"), Kind: jsonx.Str(o, "kind"), Text: jsonx.Str(o, "text"), Read: jsonx.Bool(o, "read")}
	m.Actionable = jsonx.Bool(o, "actionable")
	m.Task, m.Attempt = jsonx.Str(o, "task"), jsonx.Str(o, "attempt")
	m.Disposition, m.Reason = jsonx.Str(o, "disposition"), jsonx.Str(o, "reason")
	m.Seq, _ = jsonx.Int(seq)
	m.Round, _ = jsonx.Int(round)
	return m
}

func (m Mail) pendingFor(s *Slot) bool {
	return m.Actionable && m.Disposition == "pending" && NormID(m.Task) == NormID(s.Task()) && m.Attempt == s.Attempt()
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
		if !m.Read && (!m.Actionable || m.pendingFor(s)) {
			out = append(out, m)
		}
	}
	return out
}

// Pending holds actionable requests belonging to the current assignment.
func (s *Slot) Pending() []Mail {
	var out []Mail
	for _, m := range s.Inbox() {
		if m.pendingFor(s) {
			out = append(out, m)
		}
	}
	return out
}

// Waiting includes unread informational mail and pending requests, even read ones.
func (s *Slot) Waiting() []Mail {
	var out []Mail
	for _, m := range s.Inbox() {
		if (!m.Actionable && !m.Read) || m.pendingFor(s) {
			out = append(out, m)
		}
	}
	return out
}

// InboxNext names the inbox commands the slot still owes, built from its state:
// the --ack step only while mail is unread, the resolve step naming each
// pending request. "" when nothing is owed.
func (s *Slot) InboxNext() string {
	var steps []string
	if u := s.Unread(); len(u) > 0 {
		steps = append(steps, fmt.Sprintf("mark the %d unread mail read with `rota worker inbox %s --ack --upto %d`", len(u), s.Name(), u[len(u)-1].Seq))
	}
	if p := s.Pending(); len(p) > 0 {
		seqs := make([]string, len(p))
		for i, m := range p {
			seqs[i] = fmt.Sprint(m.Seq)
		}
		steps = append(steps, fmt.Sprintf("resolve pending request %s with `rota worker inbox %s --handled <seq> --reason <text>` (`--returned <seq>` when it belongs outside this assignment)", strings.Join(seqs, ", "), s.Name()))
	}
	return strings.Join(steps, "; ")
}

// ResolveMail records an explicit disposition without rewriting previous outcomes.
// Callers hold the registry update lock.
func (s *Slot) ResolveMail(seq int, disposition, reason string) (Mail, error) {
	if seq <= 0 || (disposition != "handled" && disposition != "returned") || strings.TrimSpace(reason) == "" {
		return Mail{}, fail(exitcode.ExitUsage, "a positive mail sequence, handled/returned disposition and nonblank reason are required")
	}
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	for _, e := range l {
		o, ok := e.(*jsonx.Object)
		if !ok || mailFrom(o).Seq != seq {
			continue
		}
		m := mailFrom(o)
		if !m.pendingFor(s) {
			return Mail{}, fail(exitcode.ExitRefused, fmt.Sprintf("mail %d is not a pending request for this assignment", seq))
		}
		o.Set("disposition", disposition)
		o.Set("read", true)
		o.Set("reason", strings.TrimSpace(reason))
		return mailFrom(o), nil
	}
	return Mail{}, fail(exitcode.ExitResolution, fmt.Sprintf("mail %d is not in the inbox", seq))
}

// endInboxAssignment hands unfinished requests back to the orchestrator. History
// keeps its original ownership; old informational mail cannot follow the new task.
func (s *Slot) endInboxAssignment() {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok {
			m := mailFrom(o)
			if m.Actionable && m.Disposition == "pending" {
				o.Set("disposition", "returned")
				o.Set("reason", "assignment ended; returned to orchestrator for routing")
			}
			o.Set("read", true)
		}
	}
}

// renameInboxTask moves the current assignment's pending requests to task, the
// new name of the same work. Mail of earlier assignments keeps its owner.
func (s *Slot) renameInboxTask(task string) {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok && mailFrom(o).pendingFor(s) {
			o.Set("task", task)
		}
	}
}

// AckUnread marks the unread mail read and returns what it marked: all of it
// when upto is 0, else only mail up to that sequence number, so a relay
// posted after the worker's listing is not acknowledged unseen. Called inside
// one registry update with the read.
func (s *Slot) AckUnread(upto int) []Mail {
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	var acked []Mail
	for _, e := range l {
		if o, ok := e.(*jsonx.Object); ok {
			m := mailFrom(o)
			if m.Read || (m.Actionable && !m.pendingFor(s)) || (upto != 0 && m.Seq > upto) {
				continue
			}
			acked = append(acked, m)
			o.Set("read", true)
		}
	}
	return acked
}

func (s *Slot) postMail(m Mail) Mail {
	next := 1
	if v, ok := s.o.Get("inboxSeq"); ok {
		if n, ok := jsonx.Int(v); ok {
			next = max(next, n+1)
		}
	}
	for _, e := range s.Inbox() {
		next = max(next, e.Seq+1)
	}
	m.Seq = next
	s.o.Set("inboxSeq", next)
	if m.Kind == MailReview || m.Kind == MailBase {
		m.Actionable, m.Task, m.Attempt, m.Disposition = true, s.Task(), s.Attempt(), "pending"
	}
	v, _ := s.o.Get("inbox")
	l, _ := v.([]any)
	s.o.Set("inbox", append(l, m.raw()))
	return m
}

func (s *Slot) retractMail(seq int) {
	hv, _ := s.o.Get("inboxSeq")
	high, _ := jsonx.Int(hv)
	for _, m := range s.Inbox() {
		high = max(high, m.Seq)
	}
	if high > 0 {
		s.o.Set("inboxSeq", high)
	}
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
