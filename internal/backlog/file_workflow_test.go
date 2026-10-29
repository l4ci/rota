package backlog

import (
	"reflect"
	"testing"
)

// File mode must satisfy no interface whose methods it only refuses: the
// status and note verbs belong to the issue backends alone.
func TestFileHasNoRefusedWorkflowVerbs(t *testing.T) {
	ft := reflect.TypeOf(&File{})
	for _, m := range []string{"Status", "NoteGet", "NotePut", "NoteRm"} {
		if _, ok := ft.MethodByName(m); ok {
			t.Errorf("File has %s: file mode would satisfy an interface by refusing it", m)
		}
	}
}

// ItemStatus is the flag form of "implements ItemTracker".
func TestItemStatusMatchesItemTracker(t *testing.T) {
	for name, be := range map[string]Backend{"file": &File{}, "issues": &Issues{}} {
		_, isTracker := be.(ItemTracker)
		if isTracker != be.Capabilities().ItemStatus {
			t.Errorf("%s: ItemTracker %v, ItemStatus %v", name, isTracker, be.Capabilities().ItemStatus)
		}
	}
}
