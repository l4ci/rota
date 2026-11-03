package escalation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const (
	DeliveryReady     = "ready"
	DeliveryUnknown   = "unknown"
	DeliveryDelivered = "delivered"
)

// Delivery is the durable payload and recovery cursor. Unknown means create may
// have applied; absence from a remote read never grants permission to resend.
type Delivery struct {
	OperationID, RequestID, Fingerprint, InputHash string
	Body, CreatedAt, State                         string
	Attempts                                       int
	LastAttemptAt, NextAttemptAt, LastError        string
	CreateAttemptAt                                string
	Timeout                                        time.Duration
}

func (d Delivery) Object() *jsonx.Object {
	o := jsonx.NewObject()
	o.Set("operationId", d.OperationID)
	o.Set("requestId", d.RequestID)
	o.Set("fingerprint", d.Fingerprint)
	o.Set("inputHash", d.InputHash)
	o.Set("body", d.Body)
	o.Set("createdAt", d.CreatedAt)
	o.Set("state", d.State)
	o.Set("attempts", d.Attempts)
	o.Set("createAttemptAt", d.CreateAttemptAt)
	o.Set("lastAttemptAt", d.LastAttemptAt)
	o.Set("nextAttemptAt", d.NextAttemptAt)
	o.Set("lastError", d.LastError)
	o.Set("timeout", d.Timeout.String())
	return o
}
func deliveryFrom(o *jsonx.Object) *Delivery {
	d := &Delivery{OperationID: jsonx.Str(o, "operationId"), RequestID: jsonx.Str(o, "requestId"), Fingerprint: jsonx.Str(o, "fingerprint"), InputHash: jsonx.Str(o, "inputHash"), Body: jsonx.Str(o, "body"), CreatedAt: jsonx.Str(o, "createdAt"), State: jsonx.Str(o, "state"), CreateAttemptAt: jsonx.Str(o, "createAttemptAt"), LastAttemptAt: jsonx.Str(o, "lastAttemptAt"), NextAttemptAt: jsonx.Str(o, "nextAttemptAt"), LastError: jsonx.Str(o, "lastError")}
	v, _ := o.Get("attempts")
	d.Attempts, _ = jsonx.Int(v)
	d.Timeout, _ = time.ParseDuration(jsonx.Str(o, "timeout"))
	return d
}

var operationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func deliveryReference(op string) string { return "Delivery reference: rota-escalation/" + op }
func hasDeliveryReference(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Delivery reference: rota-escalation/") && operationPattern.MatchString(strings.TrimPrefix(line, "Delivery reference: rota-escalation/")) {
			return true
		}
	}
	return false
}
func matchesOperation(body, op string) bool {
	for _, line := range strings.Split(body, "\n") {
		if line == deliveryReference(op) {
			return true
		}
	}
	return false
}
func inputHash(o SendOpts) string {
	o.RequestID = ""
	o.Fingerprint = ""
	b, _ := json.Marshal(o)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func reserve(env Env, root string, o SendOpts) (Entry, error) {
	var e Entry
	var refusal error
	hash := inputHash(o)
	err := worker.Update(root, func(doc *worker.Doc) {
		list := FromRegistry(doc.Registry)
		// Request identity survives the receipt-write crash, even after an answer.
		if o.RequestID != "" {
			for _, old := range list {
				if old.Delivery != nil && old.Delivery.RequestID == o.RequestID {
					if old.Delivery.Fingerprint != o.Fingerprint || old.Delivery.InputHash != hash {
						refusal = &Error{Exit: exitcode.ExitRefused, Message: "request id reused with different escalation inputs"}
						return
					}
					e = old
					return
				}
			}
		}
		if old, ok := PendingOn(list, kindOf(o.PR), o.Ref()); ok {
			if old.Delivery != nil && old.Delivery.State != DeliveryDelivered && old.Delivery.InputHash == hash {
				e = old
				return
			}
			refusal = &Error{Exit: exitcode.ExitRefused, Message: fmt.Sprintf("escalation %s is still pending on %s %s", old.ID, old.Kind, old.Ref()), Data: pendingData(old.ID)}
			return
		}
		if o.Slot != "" && doc.Slot(o.Slot) == nil {
			refusal = &Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("--slot %s is not a registered slot", o.Slot)}
			return
		}
		token := make([]byte, 16)
		if _, err := rand.Read(token); err != nil {
			refusal = err
			return
		}
		op := hex.EncodeToString(token)
		e = Entry{ID: NextID(list), Kind: kindOf(o.PR), Number: o.Number, Slot: o.Slot, Title: o.Title, Status: StatusPending, Options: append([]string(nil), o.Options...), Recommend: o.Recommend, Cost: o.Cost}
		if !o.PR {
			e.Number = 0
			if n, ok := o.issueKey().Number(); ok {
				e.Number = n
			} else {
				e.Key = o.issueKey().String()
			}
		}
		body := Compose(e.ID, o.Title, o.Body, o.Choice)
		pos := strings.IndexByte(body, '\n')
		body = body[:pos+1] + deliveryReference(op) + "\n" + body[pos+1:]
		e.Delivery = &Delivery{OperationID: op, RequestID: o.RequestID, Fingerprint: o.Fingerprint, InputHash: hash, Body: body, CreatedAt: Time(env.Now()), State: DeliveryReady, Timeout: o.Timeout}
		list = append(list, e)
		rows := make([]any, 0, len(list))
		for _, row := range list {
			rows = append(rows, row.Object())
		}
		doc.SetSection("escalations", rows)
	})
	if err != nil {
		return Entry{}, &Error{Exit: exitcode.ExitFailed, Message: "escalation intent was not written: " + err.Error()}
	}
	return e, refusal
}
func saveEntry(root string, e Entry) error {
	return Update(root, func(list []Entry) []Entry {
		for i := range list {
			if list[i].ID == e.ID { // Preserve an answer stored by a concurrent check.
				if list[i].Status == StatusAnswered {
					e.Status = list[i].Status
					e.Answer = list[i].Answer
				}
				list[i] = e
				break
			}
		}
		return list
	})
}

// retryDelay saturates without shifting or multiplying untrusted attempt counts.
func retryDelay(attempts int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempts && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
func due(d *Delivery, now time.Time) bool {
	at, err := stamp.Parse(d.NextAttemptAt)
	return err != nil || !now.Before(at)
}
func failure(root string, e Entry, now time.Time, cause error, count bool) (SendResult, error) {
	d := e.Delivery
	if count {
		if d.Attempts < int(^uint(0)>>1) {
			d.Attempts++
		}
		d.LastAttemptAt = Time(now)
	}
	d.LastError = cause.Error()
	d.NextAttemptAt = Time(now.Add(retryDelay(d.Attempts)))
	if err := saveEntry(root, e); err != nil {
		cause = fmt.Errorf("%v; recovery record could not be written: %w", cause, err)
	}
	return deliveryFailure(e, cause)
}
func deliveryFailure(e Entry, err error) (SendResult, error) {
	data := jsonx.NewObject()
	data.Set("id", e.ID)
	data.Set("escalation", e.Object())
	data.Set("changed", true)
	data.Set("commentId", e.CommentID)
	state := ""
	if e.Delivery != nil {
		state = e.Delivery.State
	}
	code := exitcode.ExitUnavailable
	var te *tracker.Error
	if errors.As(err, &te) {
		code = te.Kind.Exit()
	}
	if code == exitcode.ExitResolution {
		err = fmt.Errorf("no such %s %s: %w", e.Kind, e.Ref(), err)
	}
	var ee *Error
	if errors.As(err, &ee) {
		code = ee.Exit
	}
	return SendResult{Entry: e}, &Error{Exit: code, Message: fmt.Sprintf("escalation %s delivery %s: %v; repeat the send command or inspect with rota round escalate check %s", e.ID, state, err, e.ID), Data: data}
}

// deliver holds only the operation flock across network calls, never the registry lock.
func deliver(ctx context.Context, env Env, root string, entry Entry, allowCreate bool) (SendResult, error) {
	res := SendResult{Entry: entry}
	if entry.Delivery == nil {
		return res, nil
	}
	op := entry.Delivery.OperationID
	if !operationPattern.MatchString(op) {
		return deliveryFailure(entry, errors.New("invalid operation identity"))
	}
	var runErr error
	err := fsio.Locked(filepath.Join(filepath.Dir(worker.RegistryPath(root)), "escalations", op), fsio.LockTimeout, func() error {
		reg, err := worker.LoadRegistry(root)
		if err != nil {
			return err
		}
		var e Entry
		for _, row := range FromRegistry(reg) {
			if row.ID == entry.ID && row.Delivery != nil && row.Delivery.OperationID == op {
				e = row
				break
			}
		}
		if e.ID == "" {
			return errors.New("delivery intent disappeared")
		}
		res.Entry = e
		d := e.Delivery
		if d.State != DeliveryReady && d.State != DeliveryUnknown && d.State != DeliveryDelivered {
			return errors.New("invalid delivery state")
		}
		if d.State != DeliveryDelivered && (!due(d, env.Now()) || (d.State == DeliveryReady && !allowCreate)) {
			if allowCreate {
				res, runErr = deliveryFailure(e, fmt.Errorf("waiting until %s: %s", d.NextAttemptAt, d.LastError))
			}
			return nil
		}
		if env.Forge == nil {
			res, runErr = failure(root, e, env.Now(), errors.New("no forge configured"), true)
			return nil
		}
		f, err := env.Forge(ctx, root)
		if err != nil {
			res, runErr = failure(root, e, env.Now(), err, true)
			return nil
		}
		if d.State != DeliveryDelivered {
			cs, err := comments(ctx, f, e)
			if err != nil {
				res, runErr = failure(root, e, env.Now(), err, true)
				return nil
			}
			var matches []tracker.Comment
			for _, c := range cs {
				if matchesOperation(c.Body, op) {
					matches = append(matches, c)
				}
			}
			if len(matches) > 1 {
				d.State = DeliveryUnknown
				res, runErr = failure(root, e, env.Now(), errors.New("duplicate delivery reference conflict; no comment adopted or resent"), true)
				return nil
			}
			var sent time.Time
			if len(matches) == 1 {
				if strings.TrimSpace(matches[0].ID) == "" {
					res, runErr = failure(root, e, env.Now(), errors.New("matched comment has no id"), true)
					return nil
				}
				e.CommentID = matches[0].ID
				sent = matches[0].CreatedAt
			} else if d.State == DeliveryUnknown {
				res, runErr = failure(root, e, env.Now(), errors.New("remote outcome unresolved; delivery reference not yet visible; no resend"), true)
				return nil
			} else {
				d.State = DeliveryUnknown
				if d.Attempts < int(^uint(0)>>1) {
					d.Attempts++
				}
				d.LastAttemptAt = Time(env.Now())
				d.CreateAttemptAt = d.LastAttemptAt
				d.NextAttemptAt = Time(env.Now().Add(retryDelay(d.Attempts)))
				d.LastError = "create outcome may be unknown after interruption"
				if err := saveEntry(root, e); err != nil {
					d.State = DeliveryReady
					res, runErr = deliveryFailure(e, fmt.Errorf("create claim was not written: %w", err))
					return nil
				}
				if e.Kind == "pr" {
					e.CommentID, err = f.AddMRNote(ctx, e.Number, d.Body)
				} else {
					e.CommentID, err = f.AddComment(ctx, e.IssueKey(), d.Body)
				}
				if err != nil || strings.TrimSpace(e.CommentID) == "" {
					if err == nil {
						err = errors.New("create returned an empty comment id")
					}
					e.CommentID = ""
					res, runErr = failure(root, e, env.Now(), err, false)
					return nil
				}
			}
			if sent.IsZero() {
				sent, _ = stamp.Parse(d.CreateAttemptAt)
				if sent.IsZero() {
					sent, _ = stamp.Parse(d.LastAttemptAt)
				}
				if sent.IsZero() {
					sent, _ = stamp.Parse(d.CreatedAt)
				}
			}
			e.SentAt = Time(sent)
			if d.Timeout > 0 && e.Deadline == "" {
				e.Deadline = Time(sent.Add(d.Timeout))
			}
			d.State = DeliveryDelivered
			d.LastError = ""
			d.NextAttemptAt = ""
			if err := saveEntry(root, e); err != nil {
				// Retain the remote identity for diagnostics; disk still holds unknown.
				d.State = DeliveryUnknown
				res, runErr = deliveryFailure(e, &Error{Exit: exitcode.ExitFailed, Message: fmt.Sprintf("comment %s posted but completion was not written: %v", e.CommentID, err)})
				var url string
				var urlErr error
				if e.Kind == "pr" {
					url, urlErr = f.PRCommentURL(ctx, e.Number, e.CommentID)
				} else {
					url, urlErr = f.CommentURL(ctx, e.IssueKey(), e.CommentID)
				}
				res.URL = url
				if urlErr != nil {
					res.Warnings = append(res.Warnings, "comment posted but its url could not be read: "+urlErr.Error())
				}
				if ee, ok := runErr.(*Error); ok {
					ee.Data.(*jsonx.Object).Set("url", url)
				}
				return nil
			}
		}
		res.Entry = e
		if e.Kind == "pr" {
			res.URL, err = f.PRCommentURL(ctx, e.Number, e.CommentID)
		} else {
			res.URL, err = f.CommentURL(ctx, e.IssueKey(), e.CommentID)
		}
		if err != nil {
			res.Warnings = append(res.Warnings, "comment posted but its url could not be read: "+err.Error())
		}
		if !e.Notified {
			o := SendOpts{Number: e.Number, Key: e.IssueKey(), PR: e.Kind == "pr", Slot: e.Slot, Title: e.Title}
			e.Notified, res.Warnings = notify(ctx, env, root, e.ID, o, res.Warnings)
			if e.Notified {
				if err := saveEntry(root, e); err != nil {
					res.Warnings = append(res.Warnings, "notification raised but not recorded: "+err.Error())
				}
			}
		}
		res.Entry = e
		res.Notified = e.Notified
		return nil
	})
	if err != nil {
		return deliveryFailure(res.Entry, err)
	}
	return res, runErr
}
