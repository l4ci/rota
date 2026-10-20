package cli

import (
	"errors"
	"testing"
)

func TestDoctorLook(t *testing.T) {
	var asked []string
	lookPath := func(n string) (string, error) {
		asked = append(asked, n)
		if n == "/opt/a/tool" || n == "/abs/hook" {
			return n, nil
		}
		return "", errors.New("not found")
	}
	look := doctorLook(lookPath, "/opt/b:/opt/a")
	if p, ok := look("tool"); !ok || p != "/opt/a/tool" {
		t.Errorf("name on override: %q %v", p, ok)
	}
	if p, ok := look("/abs/hook"); !ok || p != "/abs/hook" {
		t.Errorf("path with slash: %q %v", p, ok)
	}
	if _, ok := look("none"); ok {
		t.Error("missing tool found")
	}
	asked = nil
	if p, ok := doctorLook(lookPath, "")("/abs/hook"); !ok || p != "/abs/hook" || len(asked) != 1 {
		t.Errorf("no override: %q %v %v", p, ok, asked)
	}
}
