package harness

import (
	"reflect"
	"testing"

	"github.com/l4ci/rota/internal/labels"
)

func TestRegistryHarnessNames(t *testing.T) {
	if got := labels.HarnessNames(); !reflect.DeepEqual(got, Kinds()) {
		t.Fatalf("registry harness family %v != harness kinds %v", got, Kinds())
	}
}
