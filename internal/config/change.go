package config

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/l4ci/rota/internal/change"
)

// The change.* keys: how `rota change classify`, the merge gate and the review
// depth put a diff in a tier (#2348).
const (
	ChangeDocsPathsKey  = "change.docsPaths"
	ChangeRiskyPathsKey = "change.riskyPaths"
	ChangeSmallBelowKey = "change.smallBelow"
)

// ChangePolicyOf reads the change.* keys from the merged config. An error
// names the key; callers classify as full on it rather than guess.
func ChangePolicyOf(cfg any) (change.Policy, error) {
	var p change.Policy
	var err error
	if p.DocsPaths, err = changeGlobs(cfg, ChangeDocsPathsKey); err != nil {
		return change.Policy{}, err
	}
	if p.RiskyPaths, err = changeGlobs(cfg, ChangeRiskyPathsKey); err != nil {
		return change.Policy{}, err
	}
	v, _ := Value(cfg, ChangeSmallBelowKey)
	if p.SmallBelow, err = smallBelow(v); err != nil {
		return change.Policy{}, err
	}
	return p, nil
}

// changeGlobs reads one list key of globs, each checked by change.ValidGlob.
func changeGlobs(cfg any, key string) ([]string, error) {
	v, _ := Value(cfg, key)
	return parseChangeGlobs(key, v)
}

func parseChangeGlobs(key string, v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a list of globs", key)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		g, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("%s entries must be glob strings (got %v)", key, e)
		}
		if err := change.ValidGlob(g); err != nil {
			return nil, fmt.Errorf("%s: %v", key, err)
		}
		out = append(out, g)
	}
	return out, nil
}

func smallBelow(v any) (int, error) {
	bad := fmt.Errorf("%s must be a whole number of changed lines, 0 (off) or more", ChangeSmallBelowKey)
	switch n := v.(type) {
	case nil:
		return 0, nil
	case json.Number:
		i, err := strconv.Atoi(string(n))
		if err != nil || i < 0 {
			return 0, bad
		}
		return i, nil
	case float64:
		if n < 0 || n != float64(int(n)) {
			return 0, bad
		}
		return int(n), nil
	case int:
		if n < 0 {
			return 0, bad
		}
		return n, nil
	}
	return 0, bad
}

// validateChange checks a change.* value for `config set`; ok is false when
// key is not one of them.
func validateChange(key string, value any) (ok bool, err error) {
	switch key {
	case ChangeDocsPathsKey, ChangeRiskyPathsKey:
		_, err = parseChangeGlobs(key, value)
	case ChangeSmallBelowKey:
		_, err = smallBelow(value)
	default:
		return false, nil
	}
	return true, err
}

// changeProblems lists what is wrong with the change.* keys, for config check.
func changeProblems(cfg any) []string {
	if _, err := ChangePolicyOf(cfg); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// DoorOneWay reports whether a PR body carries a "Door: one-way" line: the one
// definition review depth and the change tier share.
func DoorOneWay(body string) bool { return doorOneWay.MatchString(body) }
