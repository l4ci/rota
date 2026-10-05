// Package counter mints the zero-padded item IDs of file mode (B07, F12, M03)
// from .rota/counters.json. It sits below both backlog and milestone so
// neither has to import the other to mint an ID.
package counter

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
)

var prefixes = map[string]string{"bugs": "B", "features": "F", "tasks": "T", "milestones": "M"}

// Next bumps the counter for kind (bugs, features, tasks or milestones) in
// .rota/counters.json under root and returns the new zero-padded ID such as
// "B07". The counter never lags the highest ID already in BACKLOG.md or
// ARCHIVE.md. Existing keys keep their position and a new key is appended, so
// the file matches what the Python helper writes.
func Next(root, kind string) (string, error) {
	prefix, ok := prefixes[kind]
	if !ok {
		return "", fmt.Errorf("unknown counter kind %q (want bugs|features|tasks|milestones)", kind)
	}
	rota := func(name string) string { return rotatree.File(root, name) }
	pat := regexp.MustCompile(`\[` + prefix + `(\p{Nd}+)\]`)
	highest := 0
	for _, name := range []string{"BACKLOG.md", "ARCHIVE.md"} {
		text, err := fsio.ReadText(rota(name))
		if err != nil {
			continue
		}
		for _, m := range pat.FindAllStringSubmatch(text, -1) {
			n, err := atoi(m[1])
			if err != nil {
				return "", err
			}
			highest = max(highest, n)
		}
	}
	var next int
	err := fsio.UpdateJSON(rota("counters.json"), jsonx.NewObject(), func(v any) (any, error) {
		d, ok := v.(*jsonx.Object)
		if !ok {
			return nil, errors.New("counters.json is not a JSON object")
		}
		cur := 0
		if raw, ok := d.Get(kind); ok {
			num, _ := raw.(json.Number)
			n, err := strconv.Atoi(string(num))
			if err != nil {
				// Python's max() lets a fractional counter through when an ID
				// is higher; otherwise it writes the float and crashes. rota
				// refuses before writing.
				fl, ferr := strconv.ParseFloat(string(num), 64)
				if ferr != nil || fl >= float64(highest) {
					return nil, fmt.Errorf("counters.json: %s is not an integer", kind)
				}
				n = highest
			}
			cur = n
		}
		next = max(cur, highest) + 1
		d.Set(kind, json.Number(strconv.Itoa(next)))
		return d, nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%02d", prefix, next), nil
}

func atoi(digits string) (int, error) {
	n := 0
	for _, r := range digits {
		d := pystr.DigitValue(r)
		if n > (int(^uint(0)>>1)-d)/10 {
			return 0, fmt.Errorf("number out of range: %s", digits)
		}
		n = n*10 + d
	}
	return n, nil
}
