package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// BlameLineDates is the day of the commit that last changed each line of the
// file at rel in the working copy (index i is line i+1), from git blame; a
// line git gave no commit for is the zero time. ok is false when git cannot
// blame the file, for example when it is untracked.
func (r Repo) BlameLineDates(ctx context.Context, rel string) ([]time.Time, bool, error) {
	out, ok, err := r.Output(ctx, "blame", "--porcelain", "--", rel)
	if err != nil || !ok {
		return nil, false, err
	}
	return ParseBlameDates(out), true, nil
}

// ParseBlameDates reads `git blame --porcelain` output into the commit day of
// each line (see BlameLineDates).
func ParseBlameDates(porcelain string) []time.Time {
	var lineSha []string
	epoch := map[string]int64{}
	zone := map[string]string{}
	var cur string
	for _, l := range strings.Split(porcelain, "\n") {
		f := strings.Fields(l)
		switch {
		case strings.HasPrefix(l, "\t"):
			// file content: no metadata
		case strings.HasPrefix(l, "committer-time "):
			epoch[cur], _ = strconv.ParseInt(strings.TrimPrefix(l, "committer-time "), 10, 64)
		case strings.HasPrefix(l, "committer-tz "):
			zone[cur] = strings.TrimPrefix(l, "committer-tz ")
		case len(f) >= 3 && len(f[0]) == 40:
			n, err := strconv.Atoi(f[2])
			if err != nil || n < 1 {
				continue
			}
			cur = f[0]
			for len(lineSha) < n {
				lineSha = append(lineSha, "")
			}
			lineSha[n-1] = cur
		}
	}
	dates := make([]time.Time, len(lineSha))
	for i, sha := range lineSha {
		if sha != "" {
			dates[i] = localDay(epoch[sha], zone[sha])
		}
	}
	return dates
}

// localDay is the calendar day of an epoch time in the committer's own zone
// (+hhmm), as git log %cs reports it, at midnight UTC.
func localDay(secs int64, zone string) time.Time {
	off := int64(0)
	if len(zone) == 5 && (zone[0] == '+' || zone[0] == '-') {
		h, errH := strconv.Atoi(zone[1:3])
		m, errM := strconv.Atoi(zone[3:5])
		if errH == nil && errM == nil {
			off = int64(h*3600 + m*60)
			if zone[0] == '-' {
				off = -off
			}
		}
	}
	t := time.Unix(secs+off, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
