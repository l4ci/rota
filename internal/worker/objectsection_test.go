package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

type objRec struct {
	Name string
	N    int
}

var objSec = ObjectSection[objRec]{
	Key: "objRec",
	Decode: func(o *jsonx.Object) objRec {
		n, _ := o.Get("n")
		i, _ := jsonx.Int(n)
		return objRec{Name: jsonx.Str(o, "name"), N: i}
	},
	Encode: func(r objRec) *jsonx.Object {
		o := jsonx.NewObject()
		o.Set("name", r.Name)
		o.Set("n", r.N)
		return o
	},
}

func TestObjectSectionRoundTrip(t *testing.T) {
	root := t.TempDir()
	if got := objSec.Read(LoadRegistryTolerant(root)); got != (objRec{}) {
		t.Fatalf("missing section = %+v, want zero", got)
	}
	for i := 0; i < 3; i++ {
		if err := objSec.Update(root, func(r objRec) objRec { r.Name = "a"; r.N++; return r }); err != nil {
			t.Fatal(err)
		}
	}
	if got := objSec.Read(LoadRegistryTolerant(root)); got != (objRec{Name: "a", N: 3}) {
		t.Errorf("read = %+v, want {a 3}", got)
	}
}
