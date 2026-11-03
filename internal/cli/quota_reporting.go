package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/jsonx"
)

// quotaObject keeps the typed provider snapshot additive to existing meter and
// ledger schemas. It never manufactures headroom or quotaShare.
func quotaObject(s harness.QuotaSnapshot) (*jsonx.Object, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Decode(b)
	if err != nil {
		return nil, err
	}
	return v.(*jsonx.Object), nil
}

func nestedObject(o *jsonx.Object, key string) *jsonx.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	obj, _ := v.(*jsonx.Object)
	return obj
}

func quotaWindowText(w *jsonx.Object) string {
	if w == nil {
		return "unavailable"
	}
	value := "unavailable"
	if remaining, ok := quotaFloat(w, "remainingPercent"); ok {
		value = fmt.Sprintf("%g%% remaining", remaining)
		if used, ok := quotaFloat(w, "usedPercent"); ok {
			value += fmt.Sprintf(" (%g%% used)", used)
		}
	}
	if reset := jsonx.Str(w, "resetsAt"); reset != "" {
		value += "; resets " + reset
	} else {
		value += "; reset unavailable"
	}
	if status := jsonx.Str(w, "status"); status != "available" {
		value += "; " + status
	}
	if reason := jsonx.Str(w, "reason"); reason != "" {
		value += " (" + reason + ")"
	}
	return value
}

func accountQuotaText(q *jsonx.Object) string {
	line := "weekly: " + quotaWindowText(nestedObject(q, "weekly"))
	if reason := jsonx.Str(q, "reason"); reason != "" && nestedObject(q, "weekly") == nil {
		line += " (" + reason + ")"
	}
	for _, w := range dataObjs(q, "windows") {
		mins, ok := dataFloat(w, "durationMins")
		if ok && mins == 10080 {
			continue
		}
		label := "unknown duration"
		if ok {
			label = fmt.Sprintf("%.0fm window", mins)
		}
		line += "\n" + label + ": " + quotaWindowText(w)
	}
	line += "\nsource: " + jsonx.Str(q, "source")
	if detail := jsonx.Str(q, "sourceDetail"); detail != "" {
		line += " (" + detail + ")"
	}
	line += "; observed: " + jsonx.Str(q, "observedAt")
	line += "\nhome: " + jsonx.Str(q, "home") + "; remote identity: " + jsonx.Str(q, "identityStatus")
	if ref := jsonx.Str(q, "accountRef"); ref != "" {
		line += " " + ref
	}
	if ambient := jsonx.Bool(q, "ambient"); ambient {
		line += "; caller-current ambient account (historical identity unverified)"
	}
	return line
}

func quotaLabel(q *jsonx.Object) string {
	return strings.SplitN(accountQuotaText(q), "\n", 2)[0]
}

func quotaFloat(o *jsonx.Object, key string) (float64, bool) {
	v, _ := o.Get(key)
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		return f, err == nil
	}
	return dataFloat(o, key)
}
