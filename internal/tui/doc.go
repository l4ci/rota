// Package tui is rota's terminal screen kit: the palette and the --ui views
// run on it (spec #538). Standard library only.
//
// # The model
//
// A screen is a Model: a value with two methods.
//
//	Update(Msg) (Model, Cmd)        // one input in, the next screen out
//	Render(w, h int, st Style) string // the frame for a w x h terminal
//
// Update is pure. It receives a Key, a Tick (Terminal.Tick, for screens that
// refresh themselves) or the Done of an earlier Exec, and returns the next
// model plus a Cmd for the driver. Render draws the model and nothing else.
// Neither touches the terminal, the disk or a verb, so a test drives a screen
// by calling Update with keys and compares Strip(Render(100, 30, st)) with a
// golden frame.
//
// Anything with a side effect goes in a Cmd. Exec runs an action (a verb in
// process, a reload, another screen) and its error comes back as Done. Cooked
// hands Exec the terminal so it may print or prompt, Wait then holds its
// output on screen until a key, and Quit leaves the screen. A screen's
// actions call the verbs the CLI exposes; the UI changes nothing on its own.
//
// # The driver
//
// Run owns the terminal: raw mode through stty, the key decoder, a redraw on
// SIGWINCH, and a restore on every way out, panic and SIGINT/SIGTERM
// included. NewTerminal fills a Terminal from the process; tests fill one
// with a scripted reader and a buffer. Run returns ErrNoRaw when the
// terminal cannot take raw mode, and the caller falls back to plain output.
//
// # Widgets
//
// Widgets are values a screen embeds in its model: each has its own Update
// for the keys it understands and a Render for its part of the frame. A
// screen routes keys to the focused widget and reads its state back. They
// are List (filter, selection, scrolling), Detail (a scrolling text pane),
// the form fields Toggle, Choice and TextInput, Confirm, and Hints (the
// bottom key-hint bar). Frame and Columns lay the parts out.
//
// Style is the only way to colour text. It honours NO_COLOR and a dumb TERM,
// and Strip removes what it adds, so goldens hold plain text.
package tui
