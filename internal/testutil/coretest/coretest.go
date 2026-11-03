// Package coretest supplies an isolated JSON-RPC peer for real Core adapter tests.
package coretest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

type Server struct {
	mu                             sync.Mutex
	mode, screen, cwd, status, app string
	labels                         map[string]string
	pid, runs, kills               int
	sends                          []string
	run                            map[string]any
}

func New(t testing.TB, app string) (*Server, host.Host) {
	t.Helper()
	s := &Server{app: app, screen: "› \n", status: "idle", pid: 123, labels: map[string]string{}}
	state := t.TempDir()
	h := host.New(host.HerdrCore, host.Deps{
		Getenv: func(k string) string {
			return map[string]string{"HC_TERMINAL_ID": "t1", "HC_TERMINAL_SOCKET": "/fixture-core.sock", "ROTA_CORE_STATE_DIR": state}[k]
		},
		Dial: func(_ context.Context, path string) (net.Conn, error) {
			if path != "/fixture-core.sock" {
				return nil, fmt.Errorf("unexpected endpoint %s", path)
			}
			a, b := net.Pipe()
			go s.serve(b)
			return a, nil
		},
		Sleep: func(time.Duration) {}, Alive: func(int) bool { return false }, Tree: func(p int) []int { return []int{p, 456} },
	})
	return s, h
}
func (s *Server) Mode(mode string) { s.mu.Lock(); defer s.mu.Unlock(); s.mode = mode }
func (s *Server) Counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs, len(s.sends), s.kills
}
func (s *Server) Sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sends...)
}
func (s *Server) Run() map[string]any { s.mu.Lock(); defer s.mu.Unlock(); return s.run }
func (s *Server) Busy()               { s.mu.Lock(); defer s.mu.Unlock(); s.status = "working"; s.screen = "› \n" }
func (s *Server) terminal(addr string) map[string]any {
	pid := s.pid
	created := 123
	labels := s.labels
	cwd := s.cwd
	if addr == "t1" {
		labels = map[string]string{"herdr.space": "s1", "herdr.tab": "parent-tab"}
		pid = 90
		cwd = "/project"
	}
	if addr == "t2" && s.mode == "replaced" {
		pid = 999
	}
	if addr == "t2" && s.mode == "incarnation" {
		created = 456
	}
	state := "live"
	if addr == "t2" && s.mode == "finished" {
		state = "finished"
	}
	return map[string]any{"address": addr, "created_ms": created, "pid": pid, "cwd": cwd, "state": state, "screen": "exact", "labels": labels, "reported": true, "status": map[string]string{"app": s.app, "state": s.status}}
}
func (s *Server) serve(c net.Conn) {
	defer c.Close()
	dec := json.NewDecoder(c)
	enc := json.NewEncoder(c)
	for {
		var q struct {
			ID     int
			Method string
			Params map[string]any
		}
		if dec.Decode(&q) != nil {
			return
		}
		s.mu.Lock()
		var v any
		var rpcErr any
		switch q.Method {
		case "hello":
			v = map[string]any{"generation": 1, "version": "test-core", "core": map[string]string{"id": "test-core-id"}, "methods": []string{"terminal.run", "terminal.get", "terminal.read", "terminal.send", "terminal.kill", "terminal.list", "events.subscribe"}}
		case "terminal.get":
			v = s.terminal(q.Params["terminal"].(string))
		case "terminal.run":
			s.runs++
			s.run = q.Params
			s.cwd, _ = q.Params["cwd"].(string)
			s.labels = map[string]string{}
			for k, v := range q.Params["labels"].(map[string]any) {
				s.labels[k] = v.(string)
			}
			v = s.terminal("t2")
			if s.mode == "uncertain-run" {
				s.mu.Unlock()
				return
			}
		case "terminal.read":
			if s.mode == "unreadable" {
				rpcErr = map[string]any{"code": -32000, "message": "capture unavailable", "data": map[string]string{"kind": "unavailable"}}
			} else {
				v = map[string]any{"text": s.screen, "complete": true, "how": "screen"}
			}
		case "terminal.send":
			text := q.Params["text"].(string)
			s.sends = append(s.sends, text)
			if s.mode == "uncertain" {
				s.mu.Unlock()
				return
			}
			if s.status == "working" {
				// Codex shows only the first few queued-input lines, rather than a
				// submitted prompt echo. Keep the exact relay identity in that preview.
				lines := strings.Split(text, "\n")
				s.screen = "Queued follow-up inputs\n" + strings.Join(lines[:min(3, len(lines))], "\n") + "\n› \n"
			} else {
				s.screen = text + "\n› \n"
			}
			v = map[string]any{"turn": 1, "started": true}
		case "terminal.label":
			v = s.terminal("t2")
		case "terminal.list", "events.subscribe":
			ts := []any{s.terminal("t2")}
			if s.mode == "missing" {
				ts = []any{}
			}
			if s.mode == "unowned-same-cwd" {
				foreign := s.terminal("t-unrelated")
				foreign["labels"] = map[string]string{"herdr.space": "s1"}
				ts = []any{foreign}
			}
			if s.mode == "snapshot-unreadable" {
				rpcErr = map[string]any{"code": -32000, "message": "snapshot unavailable"}
			} else {
				v = map[string]any{"terminals": ts, "seq": 1}
			}
		case "terminal.kill":
			s.kills++
			s.mode = "finished"
			v = s.terminal("t2")
		default:
			rpcErr = map[string]any{"code": -32601, "message": "unexpected fixture RPC " + q.Method}
		}
		s.mu.Unlock()
		result := map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": v}
		if rpcErr != nil {
			delete(result, "result")
			result["error"] = rpcErr
		}
		if enc.Encode(result) != nil {
			return
		}
	}
}
