package backlog

import (
	ms "github.com/l4ci/rota/internal/milestone"
)

// mustMsStub is ms.StubOn for fixtures with fixed, valid arguments.
func mustMsStub(id, title, summary string, depends []string, today string) string {
	s, err := ms.StubOn(id, title, summary, depends, today)
	if err != nil {
		panic(err)
	}
	return s
}
