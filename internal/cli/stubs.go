package cli

import (
	"flag"
	"strings"
)

// addStubs registers every contract verb the tree lacks as a stub that exits
// 71, so a verb the contract promises answers "not implemented" (conventions:
// exit 71, until A9) instead of "unknown command" (exit 2). The list is
// contractVerbs, which a test keeps equal to the contract itself.
//
// A path is implemented when the walk reaches a real verb before running out
// of words (`block skills` is `block` with a key), or when it ends on a node
// that already exists. Only the rest become stubs, creating the group nodes
// they need.
func addStubs(root *Command) {
	for _, path := range contractVerbs {
		words := strings.Fields(path)
		cmd := root
		for i, w := range words {
			next := cmd.sub(w)
			if next == nil {
				// the rest of the path is new
				for _, name := range words[i:] {
					n := &Command{Name: name, Summary: "not ported yet (exit 71)"}
					cmd.Subs = append(cmd.Subs, n)
					cmd = n
				}
				cmd.Stub = true
				cmd.Verb = stubVerb
				break
			}
			cmd = next
			if cmd.Verb != nil && !cmd.Stub && i < len(words)-1 {
				break // extra words are the verb's positional arguments
			}
		}
	}
}

// stubVerb is never run (run answers stubs first); it only marks the node as
// a verb so the tree and help treat it as one.
func stubVerb(*flag.FlagSet) RunFunc {
	return func(*Ctx, []string) (Result, error) { return Result{}, NotImplemented("stub") }
}
