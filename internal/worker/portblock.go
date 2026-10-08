package worker

import (
	"regexp"
	"strconv"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

// Default work.portBase and work.portBlock: slot n owns ports
// portBase+n*portBlock up to the next block.
const (
	DefaultPortBase  = 20000
	DefaultPortBlock = 100
)

// PortBase is the first port of the slot's reserved block, 0 for none.
func (s *Slot) PortBase() int {
	v, _ := s.o.Get("portBase")
	n, _ := jsonx.Int(v)
	return n
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
// (reap): the lowest block no other slot holds, picked under the registry
// lock, so two live slots never share one. A slot that already has a block
// keeps it.
func EnsurePortBase(root, name string, base, block int) (int, error) {
	got := 0
	err := Update(root, func(d *Doc) {
		s := d.Slot(name)
		if s == nil {
			return
		}
		if got = s.PortBase(); got > 0 {
			return
		}
		taken := map[int]bool{}
		for _, o := range d.Slots() {
			if p := o.PortBase(); p > 0 {
				taken[p] = true
			}
		}
		got = base
		for taken[got] {
			got += block
		}
		s.o.Set("portBase", got)
	})
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

// exportPrefix is SlotEnv as a shell prefix. The values are identifier-safe,
// so no quoting is needed.
func exportPrefix(name string, portBase int) string {
	p := ""
	for _, e := range SlotEnv(name, portBase) {
		p += "export " + e + "; "
	}
	return p
}
