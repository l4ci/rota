package release

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
)

// LogFormat is the `git log --pretty` format CommitNotes parses.
const LogFormat = "format:%H%x09%s%x09%b%x1e"

var bucketOrder = []string{"Breaking", "New", "Fixed", "Performance", "Changed", "Documentation", "Other"}

type prefixRule struct {
	re     *regexp.Regexp
	bucket string // "" skips the commit
}

var prefixRules = []prefixRule{
	{regexp.MustCompile(`^feat(?:ure)?(\(.+?\))?:`), "New"},
	{regexp.MustCompile(`^fix(\(.+?\))?:`), "Fixed"},
	{regexp.MustCompile(`^perf(\(.+?\))?:`), "Performance"},
	{regexp.MustCompile(`^refactor(\(.+?\))?:`), "Changed"},
	{regexp.MustCompile(`^chore(\(.+?\))?:`), "Changed"},
	{regexp.MustCompile(`^style(\(.+?\))?:`), "Changed"},
	{regexp.MustCompile(`^docs(\(.+?\))?:`), "Documentation"},
	{regexp.MustCompile(`^test(\(.+?\))?:`), ""},
}

var breakingRe = regexp.MustCompile(`(?i)BREAKING CHANGE:`)

// CommitNotes is hv-release-changelog-from-commits: it buckets the commits
// of a `git log --pretty=LogFormat` dump by Conventional Commits prefix and
// returns what the helper printed (empty when nothing is worth listing),
// with `## X` headings as the old helper wrote them.
func CommitNotes(raw string) string {
	buckets := map[string][]string{}
	count := 0
	for _, rec := range strings.Split(raw, "\x1e") {
		rec = pystr.Strip(rec)
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		hash, subject := pystr.Strip(parts[0]), pystr.Strip(parts[1])
		body := ""
		if len(parts) > 2 {
			body = pystr.Strip(parts[2])
		}
		if hash == "" || subject == "" {
			continue
		}
		count++
		short := hash
		if len(short) > 7 {
			short = short[:7]
		}
		bucket, desc, scope, skipped := "Other", subject, "", false
		for _, r := range prefixRules {
			m := r.re.FindStringSubmatchIndex(subject)
			if m == nil {
				continue
			}
			if r.bucket == "" {
				skipped = true
				break
			}
			bucket = r.bucket
			desc = pystr.Strip(subject[m[1]:])
			if m[2] >= 0 {
				scope = strings.Trim(subject[m[2]:m[3]], "()")
			}
			break
		}
		if skipped {
			continue
		}
		line := fmt.Sprintf("- %s (`%s`)", desc, short)
		if scope != "" {
			line = fmt.Sprintf("- %s (%s) (`%s`)", desc, scope, short)
		}
		buckets[bucket] = append(buckets[bucket], line)
		if breakingRe.MatchString(body) || breakingRe.MatchString(subject) {
			buckets["Breaking"] = append(buckets["Breaking"], line)
		}
	}
	var parts []string
	for _, b := range bucketOrder {
		if len(buckets[b]) > 0 {
			parts = append(parts, "## "+b+"\n\n"+strings.Join(buckets[b], "\n"))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n") + "\n" + fmt.Sprintf("\n## Stats\n\n%d commits.\n", count)
}

var h2Re = regexp.MustCompile(`(?m)^## `)

// ToH3 turns `## X` headings into `### X`, as the notes go under the
// `## v<version>` changelog heading.
func ToH3(markdown string) string { return h2Re.ReplaceAllString(markdown, "### ") }
