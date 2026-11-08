package exitmap

import (
	"errors"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

// translateConfig maps the config error families in o.Classes. ok is false
// when err is none of them.
func translateConfig(err error, o Options) (data any, out error, ok bool) {
	if o.Classes&ConfigRefusal != 0 {
		if d, e, hit := configRefusal(err); hit {
			return d, e, true
		}
	}
	if o.Classes&ConfigUsage != 0 && configUsage(err) {
		return nil, &exitcode.Error{Exit: exitcode.ExitUsage, Message: err.Error()}, true
	}
	if o.Classes&ConfigCorrupt != 0 && errors.Is(err, config.ErrCorrupt) {
		return nil, &exitcode.Error{Exit: exitcode.ExitInternal, Message: err.Error()}, true
	}
	if o.Classes&ConfigInternal != 0 && configInternal(err) {
		return nil, &exitcode.Error{Exit: exitcode.ExitInternal, Message: err.Error()}, true
	}
	return nil, nil, false
}

// configUsage are the config errors that are the caller's input (exit 2).
func configUsage(err error) bool {
	return errors.Is(err, config.ErrMalformedKey) || errors.Is(err, config.ErrNotSchemaKey) ||
		errors.Is(err, config.ErrBadValue) || errors.Is(err, config.ErrProjectOwned)
}

// configInternal are a config file that is not a JSON object, or one rota
// refuses to read as empty (exit 70, naming the file).
func configInternal(err error) bool {
	return errors.Is(err, config.ErrNotObject) || errors.Is(err, config.ErrLocalNotObject) ||
		errors.Is(err, config.ErrGlobalNotObject) ||
		errors.Is(err, fsio.ErrUnreadable)
}

// configRefusal maps the migration conflicts: disagreeing old and new names
// refuse (exit 4) and list both values.
func configRefusal(err error) (any, error, bool) {
	var ce *config.ConflictError
	var ae *config.AccountConflictError
	var me *config.MergeConflictError
	var d any
	switch {
	case errors.As(err, &me):
		list := []any{}
		for _, x := range me.Conflicts {
			list = append(list, jsonObj("file", x.File, "old", x.Old, "new", x.New))
		}
		d = jsonObj("blockedBy", blockedby.MergeConflict, "conflicts", list, "changed", false)
	case errors.As(err, &ae):
		list := []any{}
		for _, x := range ae.Conflicts {
			list = append(list, jsonObj("file", x.File, "name", x.Name, "dirs", []any{x.A, x.B}))
		}
		d = jsonObj("blockedBy", blockedby.AccountConflict, "conflicts", list, "changed", false)
	case errors.As(err, &ce):
		list := []any{}
		for _, x := range ce.Conflicts {
			list = append(list, jsonObj("file", x.File, "from", x.Old, "to", x.New, "fromValue", x.OldValue, "toValue", x.NewValue))
		}
		d = jsonObj("blockedBy", blockedby.ConfigConflict, "conflicts", list, "changed", false)
	default:
		return nil, nil, false
	}
	return d, &exitcode.Error{Exit: exitcode.ExitRefused, Message: err.Error()}, true
}
