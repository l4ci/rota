package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tui"
)

func refreshMaintainerSnapshot(ctx context.Context, c *Ctx, root string, env round.Env) ([]round.MaintainerWait, []string) {
	waits, warnings := env.NeedsYou(ctx, root)
	if len(warnings) > 0 {
		return waits, warnings
	}
	commonDir, err := rotastate.CommonDirVia(ctx, c.deps().Git, root)
	if errors.Is(err, rotastate.ErrNotRepo) {
		return waits, warnings // status also serves tracker-only projects
	}
	if err == nil {
		err = roundwatch.SaveMaintainerSnapshot(commonDir, c.deps().Now(), len(waits))
	}
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("maintainer snapshot: %v", err))
	}
	return waits, warnings
}

func maintainerRows(waits []round.MaintainerWait, now time.Time) ([]any, []string) {
	rows := make([]any, 0, len(waits))
	var lines []string
	for _, wait := range waits {
		o := jsonx.NewObject()
		o.Set("kind", wait.Kind)
		o.Set("item", wait.Item)
		o.Set("ask", wait.Ask)
		o.Set("since", wait.Since)
		o.Set("ageSeconds", nil)
		age := "unknown"
		if since, err := time.Parse(time.RFC3339, wait.Since); err == nil {
			seconds := max(int64(now.Sub(since)/time.Second), 0)
			o.Set("ageSeconds", seconds)
			age = (time.Duration(seconds) * time.Second).String()
		}
		o.Set("defaultReply", wait.DefaultReply)
		rows = append(rows, o)
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", tui.Sanitize(wait.Item, false), strings.Join(strings.Fields(tui.Sanitize(wait.Ask, false)), " "), age, wait.DefaultReply))
	}
	return rows, lines
}
