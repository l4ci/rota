# Test quality

Rules for whoever writes a test: `/rota-work` workers and their subagents, rota-plan Verify steps, dispatched round workers. `/rota-review` grades against the same definitions (`rota-review/reviewer-briefs.md`, rubric item 3), so a test that breaks them costs a bounce.

## Tautological test

The expected value is computed the way the code computes it, so the test cannot disagree with the code. Pin the expected value by hand.

```go
// bad: reuses the code under test to build the answer
func TestTotal(t *testing.T) {
	got := Total(items)
	if got != sumPrices(items)*(1+TaxRate) { t.Fatal("wrong total") }
}

// good: a literal worked out independently
func TestTotal(t *testing.T) {
	got := Total([]Item{{Price: 1000}, {Price: 500}}) // 19% tax
	if got != 1785 { t.Fatalf("got %d, want 1785", got) }
}
```

## Implementation-coupled test

Pinned to internals or call order instead of behaviour, so a correct refactor breaks it. Assert on what a caller can observe: return values, written files, emitted output.

```go
// bad: passes only while Save calls these helpers in this order
func TestSave(t *testing.T) {
	spy := &spyStore{}
	Save(spy, doc)
	if !slices.Equal(spy.calls, []string{"validate", "encode", "write"}) { t.Fatal(spy.calls) }
}

// good: saves, then reads back through the public path
func TestSave(t *testing.T) {
	s := newTempStore(t)
	if err := Save(s, doc); err != nil { t.Fatal(err) }
	if got, _ := Load(s, doc.ID); got != doc { t.Fatalf("got %v, want %v", got, doc) }
}
```

## Mock only at boundaries

Mock what the process does not own: network, other processes, the clock, randomness. Use a real temp dir, a real struct, a real in-package collaborator. Mocking your own packages freezes their current shape into the test and turns it implementation-coupled.

## One slice at a time

Write one test, watch it fail on an assertion, write the code that passes it, then the next test. Do not write all the tests first: a batch of tests written before any code pins imagined behaviour. The failing run is the RED row the contract asks for.
