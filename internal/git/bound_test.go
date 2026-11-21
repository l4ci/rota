package git

import (
	"context"
	"reflect"
	"testing"
)

func TestRunnerInBindsContextAndDir(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	var gotCtx context.Context
	var gotDir string
	var gotArgs []string
	var r Runner = func(c context.Context, dir string, args ...string) (Result, error) {
		gotCtx, gotDir, gotArgs = c, dir, args
		return Result{Stdout: "ok"}, nil
	}

	res, err := r.In(ctx, "/scratch")("status", "--porcelain")

	if err != nil || res.Stdout != "ok" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotCtx.Value(ctxKey{}) != "marker" {
		t.Errorf("context not forwarded")
	}
	if gotDir != "/scratch" {
		t.Errorf("dir = %q, want /scratch", gotDir)
	}
	if want := []string{"status", "--porcelain"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
}
