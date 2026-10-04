package backlog

import "strings"

// Workflow is the part of a backlog that coordinates workers on an item: the
// claim lock, the state label, the status read-back and the durable notes.
// Both backends implement it; the file backend keeps the lock in status.json,
// so its claim, release and state verbs are successful no-ops and its status
// and note verbs are refused as issue-only (ErrWrongBackend).
type Workflow interface {
	// Claim takes the item for claimID. won is false when another claim holds
	// it; holder is then that claim's ID. changed is false when the backend
	// has no lock to take (file mode).
	Claim(ref, claimID string) (won bool, holder string, err error)
	// Release gives the item back; false when claimID held no open claim.
	Release(ref, claimID string) (bool, error)
	// SetState leaves exactly one state label (in-progress, needs-review,
	// changes-requested) or none ("none"); false when nothing changed.
	SetState(ref, state string) (bool, error)
	// Status is the read-back of the item; the file backend refuses it.
	Status(ref string) (*Status, error)
	// NoteGet returns the durable note of a kind (proof, design, plan or
	// plan:S<NN>); ok is false when the item has none.
	NoteGet(ref, kind string) (text string, ok bool, err error)
	// NotePut writes the note; false when it already read as text.
	NotePut(ref, kind, text string) (bool, error)
	// NoteRm deletes the note; false when there was none.
	NoteRm(ref, kind string) (bool, error)
}

var (
	_ Workflow = (*File)(nil)
	_ Workflow = (*Issues)(nil)
)

// fileNoteHint names where a file-mode project keeps what issue mode keeps in
// notes (_FILE_NOTE_MSG); fileShowHint where it keeps what item show reads.
const (
	fileNoteHint = "rota design or rota plan"
	fileShowHint = "see BACKLOG.md, status.json and the detail file"
)

func fileIssueOnly(what, hint string) error {
	return &RefusedError{BlockedBy: "backend", Err: ErrWrongBackend, Hint: hint,
		Msg: what + ` is not available with backlog.backend "file"`}
}

func (f *File) exists(ref string) error {
	if _, err := f.Get(ref); err != nil {
		return err
	}
	return nil
}

// Claim is a no-op in file mode: status.json is the lock. The item must exist.
func (f *File) Claim(ref, claimID string) (bool, string, error) {
	return true, claimID, f.exists(ref)
}

// Release is a no-op in file mode.
func (f *File) Release(ref, claimID string) (bool, error) { return false, f.exists(ref) }

// SetState is a no-op in file mode.
func (f *File) SetState(ref, state string) (bool, error) {
	if _, ok := StateRoleFor[state]; !ok {
		return false, errf(ErrInvalid, "state must be one of %s", strings.Join(States, "/"))
	}
	return false, f.exists(ref)
}

// Status is refused in file mode: the state lives in BACKLOG.md, status.json
// and the detail file.
func (f *File) Status(string) (*Status, error) { return nil, fileIssueOnly("item show", fileShowHint) }

// NoteGet is refused in file mode.
func (f *File) NoteGet(string, string) (string, bool, error) {
	return "", false, fileIssueOnly("item note show", fileNoteHint)
}

// NotePut is refused in file mode.
func (f *File) NotePut(string, string, string) (bool, error) {
	return false, fileIssueOnly("item note add", fileNoteHint)
}

// NoteRm is refused in file mode.
func (f *File) NoteRm(string, string) (bool, error) {
	return false, fileIssueOnly("item note rm", fileNoteHint)
}
