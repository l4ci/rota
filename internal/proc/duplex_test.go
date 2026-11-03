package proc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDuplexHandshake(t *testing.T) {
	called := false
	err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", `printf 'ready\n'; read answer; printf 'reply:%s\n' "$answer"`}}, func(r io.Reader, w io.Writer) error {
		called = true
		br := bufio.NewReader(r)
		line, err := br.ReadString('\n')
		if err != nil || line != "ready\n" {
			return fmt.Errorf("greeting %q: %v", line, err)
		}
		if _, err := io.WriteString(w, "hello\n"); err != nil {
			return err
		}
		line, err = br.ReadString('\n')
		if err != nil || line != "reply:hello\n" {
			return fmt.Errorf("reply %q: %v", line, err)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("exchange called=%v, error=%v", called, err)
	}
}

func TestDuplexDeadlineUnblocksIO(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			started := time.Now()
			called := false
			err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "exec sleep 30"}, Timeout: 100 * time.Millisecond, Group: true}, func(r io.Reader, w io.Writer) error {
				called = true
				if operation == "read" {
					_, err := io.ReadAll(r)
					return err
				}
				_, err := io.Copy(w, strings.NewReader(strings.Repeat("x", 2<<20)))
				return err
			})
			if !called || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("callback=%v error=%v", called, err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("deadline did not promptly close blocked IO")
			}
		})
	}
}

func TestDuplexOutputBudgetAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name             string
		count, cap, want int
		limited          bool
	}{
		{"exact cap reaches EOF", 8, 8, 8, false},
		{"one byte over", 9, 8, 8, true},
		{"default budget", 1048577, 0, 1048576, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []byte
			err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", fmt.Sprintf("head -c %d /dev/zero", tc.count)}, MaxOutput: tc.cap, Group: true}, func(r io.Reader, _ io.Writer) error {
				var err error
				got, err = io.ReadAll(r)
				return err
			})
			if len(got) != tc.want || errors.Is(err, ErrOutputLimit) != tc.limited || (!tc.limited && err != nil) {
				t.Fatalf("read %d bytes, error=%v; want %d bytes, limited=%v", len(got), err, tc.want, tc.limited)
			}
		})
	}
}

func TestDuplexBudgetCountsAlreadyConsumedBytes(t *testing.T) {
	err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", `printf 1234; read answer; printf 56789`}, MaxOutput: 8}, func(r io.Reader, w io.Writer) error {
		first := make([]byte, 4)
		if _, err := io.ReadFull(r, first); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "next\n"); err != nil {
			return err
		}
		_, err := io.ReadAll(r)
		return err
	})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("consumed bytes escaped the total budget: %v", err)
	}
}

func TestDuplexCompletionReapsLiveChild(t *testing.T) {
	for _, callbackErr := range []error{nil, errors.New("protocol refused")} {
		var pid int
		err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", `echo $$; exec sleep 30`}, Group: true}, func(r io.Reader, _ io.Writer) error {
			if _, err := fmt.Fscanln(r, &pid); err != nil {
				return err
			}
			return callbackErr
		})
		if !errors.Is(err, callbackErr) || pid == 0 {
			t.Fatalf("pid=%d error=%v, want %v", pid, err, callbackErr)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("child %d still exists after return: %v", pid, err)
		}
	}
}

func TestDuplexOutputLimitUnblocksWriter(t *testing.T) {
	err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "printf 123456789; exec sleep 30"}, MaxOutput: 8, Timeout: 3 * time.Second, Group: true}, func(_ io.Reader, w io.Writer) error {
		_, err := io.Copy(w, strings.NewReader(strings.Repeat("x", 2<<20)))
		return err
	})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("stdout overflow did not interrupt write: %v", err)
	}
}

func TestDuplexDiscardsStderrEvenWhenCombined(t *testing.T) {
	var got []byte
	err := Duplex(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "head -c 100000 /dev/zero >&2; printf ok"}, Combined: true, MaxOutput: 2}, func(r io.Reader, _ io.Writer) error {
		var err error
		got, err = io.ReadAll(r)
		return err
	})
	if err != nil || string(got) != "ok" {
		t.Fatalf("stdout=%q error=%v", got, err)
	}
}

func TestDuplexParentCancellationUnblocksRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Duplex(ctx, Cmd{Name: "sh", Args: []string{"-c", "printf ready; exec sleep 30"}, Timeout: NoTimeout}, func(r io.Reader, _ io.Writer) error {
		ready := make([]byte, 5)
		if _, err := io.ReadFull(r, ready); err != nil {
			return err
		}
		cancel()
		_, err := io.ReadAll(r)
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestDuplexMissingBinary(t *testing.T) {
	err := Duplex(context.Background(), Cmd{Name: "rota-no-such-binary"}, func(io.Reader, io.Writer) error {
		t.Fatal("callback ran without a child")
		return nil
	})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("error=%v", err)
	}
}
