package cycle

import "testing"

func TestIsSibling(t *testing.T) {
	tracked := func(p string) bool { return p == "Foo.gd" || p == "src/main.go" || p == "App.swift" }
	for _, c := range []struct {
		path string
		want Kind
	}{
		{"Foo.gd.uid", ToolSibling},
		{"scenes/New.gd.uid", ToolSibling},
		{"Foo.gd.import", ToolSibling}, // beside tracked Foo.gd
		{".DS_Store", ToolSibling},
		{"sub/dir/.DS_Store", ToolSibling},
		{"App.xcworkspace/contents.xcworkspacedata", ToolSibling},
		{"App.xcodeproj/project.pbxproj", ToolSibling},
		{"Package.resolved", ToolSibling},
		{"ios/Package.resolved", ToolSibling},
		{".rota/designs/B07.md", Artifact},
		{".rota/plans/B07.md", Artifact},
		{".rota/designs/B08.md", User},
		{".rota/plans/M01-B07.md", User},
		{"src/main.go", User},
		{"src/other.go.bak", User}, // main.go is tracked, other.go is not
		{"notes.txt", User},
		{"project.pbxproj", User},
	} {
		if got := Classify(c.path, tracked, []string{"B07"}); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.path, got, c.want)
		}
		if got := IsSibling(c.path, tracked, []string{"B07"}); got != (c.want != User) {
			t.Errorf("IsSibling(%q) = %v", c.path, got)
		}
	}
}

func TestIsSiblingNilTracked(t *testing.T) {
	if IsSibling("Foo.gd.import", nil, nil) {
		t.Fatal("nothing is tracked: not a sibling")
	}
}
