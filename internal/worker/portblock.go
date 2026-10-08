package worker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
)

// Default work.portBase and work.portBlock: a slot owns portBlock ports from
// its first port; new slots take the lowest free range from portBase up.
const (
	DefaultPortBase  = 20000
	DefaultPortBlock = 100
	maxPort          = 65535
)

// PortBase is the first port of the slot's reserved block, 0 for none.
func (s *Slot) PortBase() int {
	v, _ := s.o.Get("portBase")
	n, _ := jsonx.Int(v)
	return n
}

// PortBlock is the width of the slot's reserved block, the work.portBlock in
// force when it was allocated, so a later config change cannot shrink or grow
// a live slot's claim. 0 when the slot holds none.
func (s *Slot) PortBlock() int {
	if s.PortBase() == 0 {
		return 0
	}
	v, _ := s.o.Get("portBlock")
	if n, _ := jsonx.Int(v); n > 0 {
		return n
	}
	return DefaultPortBlock
}

// ReleasePort drops the slot's port block.
func (s *Slot) ReleasePort() {
	s.o.Set("portBase", nil)
	s.o.Set("portBlock", nil)
}

// PortRange reads work.portBase and work.portBlock from cfg.
func PortRange(cfg any) (base, block int) {
	base, block = DefaultPortBase, DefaultPortBlock
	if n, err := config.Int(cfg, "work.portBase", 1, 65535); err == nil && n > 0 {
		base = n
	}
	if n, err := config.Int(cfg, "work.portBlock", 1, 65535); err == nil && n > 0 {
		block = n
	}
	return base, block
}

// EnsurePortBase reserves the slot's port block in the registry and returns
// its first port. The block belongs to the slot until it leaves the registry
// (reap) or is reclaimed: the lowest range of block ports no other slot's
// range overlaps, picked under the registry lock, so two live slots never
// share a port even after work.portBase or work.portBlock change (each slot
// keeps the width it was allocated with). A slot that already has a block
// keeps it. The range must end at or below 65535.
func EnsurePortBase(root, name string, base, block int) (int, error) {
	got := 0
	var ferr error
	err := Update(root, func(d *Doc) {
		s := d.Slot(name)
		if s == nil {
			return
		}
		if got = s.PortBase(); got > 0 {
			return
		}
		got = base
		for moved := true; moved; {
			moved = false
			for _, o := range d.Slots() {
				if o.Name() == name || o.PortBase() == 0 {
					continue
				}
				if end := o.PortBase() + o.PortBlock(); got < end && o.PortBase() < got+block {
					got, moved = end, true
				}
			}
		}
		if got+block-1 > maxPort {
			ferr = fmt.Errorf("no free port block for slot %s: %d ports from %d would pass %d; lower work.portBase or work.portBlock", name, block, got, maxPort)
			got = 0
			return
		}
		s.o.Set("portBase", got)
		s.o.Set("portBlock", block)
	})
	if err == nil {
		err = ferr
	}
	return got, err
}

var notIdent = regexp.MustCompile(`[^A-Za-z0-9]`)

// SlotEnv is the environment a slot's agent and work.envSetup see:
// the slot name, the first port of its block, and a database-name suffix.
func SlotEnv(name string, portBase int) []string {
	return []string{
		"ROTA_SLOT=" + name,
		"ROTA_PORT_BASE=" + strconv.Itoa(portBase),
		"ROTA_DB_SUFFIX=_" + notIdent.ReplaceAllString(name, "_"),
	}
}

// exportPrefix is SlotEnv as a shell prefix. Slot names are free text, so
// each value is single-quoted.
func exportPrefix(name string, portBase int) string {
	p := ""
	for _, e := range SlotEnv(name, portBase) {
		k, v, _ := strings.Cut(e, "=")
		p += "export " + k + "=" + hook.ShellQuote(v) + "; "
	}
	return p
}
