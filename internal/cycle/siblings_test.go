package cycle

import "testing"

func TestClassify(t *testing.T) {
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
		{"src/main.go.orig", User}, // beside tracked main.go, but not a toolchain suffix
		{"App.swift.local", User},
		{"notes.txt", User},
		{"project.pbxproj", User},
	} {
		if got := Classify(c.path, tracked, []string{"B07"}); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestClassifyNilTracked(t *testing.T) {
	if got := Classify("Foo.gd.import", nil, nil); got != User {
		t.Fatalf("nothing is tracked: Classify = %v, want User", got)
	}
}
