package exitcode

import (
	"fmt"
	"testing"
)

type payload struct{ N int }

func TestDataOf(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &Error{Exit: ExitRefused, Message: "no", Data: payload{N: 7}})
	if d, ok := DataOf[payload](err); !ok || d.N != 7 {
		t.Errorf("DataOf through a wrap = %+v, %v", d, ok)
	}
	if _, ok := DataOf[string](err); ok {
		t.Error("a different type must not match")
	}
	if _, ok := DataOf[payload](fmt.Errorf("plain")); ok {
		t.Error("a plain error carries no data")
	}
	if _, ok := DataOf[payload](&Error{Exit: ExitRefused}); ok {
		t.Error("an Error without data carries none")
	}
}
