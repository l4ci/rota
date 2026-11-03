package host

import (
	"context"
	"fmt"
	"time"
)

// Core's plain subscription sends its authoritative snapshot in the response
// before notifications. Reconnect always subscribes afresh; raw turn counters
// deliberately do not implement TurnReader or imply successful completion.
type coreWatch struct {
	h       *coreHost
	c       *coreConn
	seq     uint64
	targets map[string]WatchTarget
	handles map[string]coreHandle
	pending []string
}

func (h *coreHost) Watch(ctx context.Context, targets []WatchTarget) (Watch, error) {
	w := &coreWatch{h: h, targets: map[string]WatchTarget{}, handles: map[string]coreHandle{}}
	var endpoint, id string
	for _, t := range targets {
		v, e := h.owned(t.Handle)
		if e != nil {
			return nil, e
		}
		if endpoint != "" && (v.Endpoint != endpoint || v.Core != id) {
			return nil, fmt.Errorf("Core watch targets belong to different endpoints")
		}
		endpoint, id = v.Endpoint, v.Core
		w.targets[v.Address] = t
		w.handles[v.Address] = v
	}
	if len(targets) == 0 {
		return w, nil
	}
	c, e := h.connect(ctx, endpoint, id)
	if e != nil {
		return nil, e
	}
	w.c = c
	var snapshot struct {
		Seq       uint64
		Terminals *[]coreTerminal
	}
	if e = c.call("events.subscribe", map[string]any{}, &snapshot); e != nil {
		c.Close()
		return nil, e
	}
	if snapshot.Terminals == nil {
		c.Close()
		return nil, fmt.Errorf("Core subscription omitted snapshot")
	}
	w.seq = snapshot.Seq
	for _, t := range *snapshot.Terminals {
		if v, ok := w.handles[t.Address]; ok {
			if coreProgram(v, t) != nil {
				if e = h.invalidate(v); e != nil {
					c.Close()
					return nil, e
				}
			} else if e = h.remember(v, t); e != nil {
				c.Close()
				return nil, e
			}
		}
	}
	for _, t := range targets {
		w.pending = append(w.pending, t.Slot)
	}
	return w, nil
}
func (w *coreWatch) Next(ctx context.Context) (string, error) {
	if len(w.pending) > 0 {
		s := w.pending[0]
		w.pending = w.pending[1:]
		return s, nil
	}
	if w.c == nil {
		<-ctx.Done()
		return "", ctx.Err()
	}
	for {
		deadline := time.Now().Add(45 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		w.c.SetReadDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { w.c.SetReadDeadline(time.Now()) })
		var n struct {
			Method string
			Params struct {
				Seq            uint64
				Type, Terminal string
			}
		}
		e := w.c.dec.Decode(&n)
		stop()
		if e != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: %v", ErrStreamLost, e)
		}
		if n.Method == "missed" {
			return "", ErrStreamLost
		}
		if n.Method != "event" || n.Params.Seq <= w.seq {
			continue
		}
		w.seq = n.Params.Seq
		target, ok := w.targets[n.Params.Terminal]
		if !ok {
			continue
		}
		if n.Params.Type == "lost" || n.Params.Type == "restarted" {
			if e = w.h.invalidate(w.handles[n.Params.Terminal]); e != nil {
				return "", e
			}
		}
		return target.Slot, nil
	}
}
func (w *coreWatch) Close() {
	if w.c != nil {
		w.c.Close()
	}
}
