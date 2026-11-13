package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Tokens are the token totals of one session.
type Tokens struct{ Input, Output, CacheRead, CacheWrite int64 }

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
}

// TranscriptUsage is the token total of the session transcript at path, which
// the harness reported, and the subagent transcripts beside it. A kind without
// a usage reader, or a transcript that cannot be read, is an error: unknown,
// never zero.
func TranscriptUsage(kind, path string) (Tokens, []string, error) {
	switch kind {
	case "claude":
		return ClaudeUsage(path)
	case "codex":
		t, err := CodexUsage(path)
		return t, nil, err
	}
	return Tokens{}, nil, errors.New("no usage reader for harness " + kind)
}

// eachLine calls fn with every line of the file; long lines are fine.
func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// claudeFileUsage sums the usage of a Claude transcript. A streamed message
// is written several times under one id, so each id counts once.
func claudeFileUsage(path string) (Tokens, error) {
	var t Tokens
	seen := map[string]bool{}
	err := eachLine(path, func(line []byte) {
		var l struct {
			Message struct {
				ID    string `json:"id"`
				Usage *struct {
					In  int64 `json:"input_tokens"`
					Out int64 `json:"output_tokens"`
					CR  int64 `json:"cache_read_input_tokens"`
					CW  int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) != nil || l.Message.Usage == nil {
			return
		}
		if id := l.Message.ID; id != "" {
			if seen[id] {
				return
			}
			seen[id] = true
		}
		u := l.Message.Usage
		t.add(Tokens{Input: u.In, Output: u.Out, CacheRead: u.CR, CacheWrite: u.CW})
	})
	return t, err
}

// ClaudeUsage is the token total of a Claude session transcript and of the
// subagent transcripts beside it (<transcript minus .jsonl>/subagents/agent-*.jsonl),
// with the subagent paths. A transcript that cannot be read is an error.
func ClaudeUsage(path string) (Tokens, []string, error) {
	total, err := claudeFileUsage(path)
	if err != nil {
		return Tokens{}, nil, err
	}
	subs, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-*.jsonl"))
	for _, s := range subs {
		t, err := claudeFileUsage(s)
		if err != nil {
			return Tokens{}, nil, err
		}
		total.add(t)
	}
	return total, subs, nil
}

// CodexUsage is the last cumulative token_count of a Codex rollout. A rollout
// with none is an error, not zero.
func CodexUsage(path string) (Tokens, error) {
	var t Tokens
	found := false
	err := eachLine(path, func(line []byte) {
		var l struct {
			Payload struct {
				Type string `json:"type"`
				Info *struct {
					Total struct {
						In  int64 `json:"input_tokens"`
						CR  int64 `json:"cached_input_tokens"`
						CW  int64 `json:"cache_write_input_tokens"`
						Out int64 `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) != nil || l.Payload.Type != "token_count" || l.Payload.Info == nil {
			return
		}
		u := l.Payload.Info.Total
		t, found = Tokens{Input: u.In, Output: u.Out, CacheRead: u.CR, CacheWrite: u.CW}, true
	})
	if err != nil {
		return Tokens{}, err
	}
	if !found {
		return Tokens{}, errors.New("no token_count event in " + path)
	}
	return t, nil
}
