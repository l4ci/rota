package hook

import (
	"strings"
)

func (g *guard) gh(args []string) error {
	var pos []string
	for i := 0; i < len(args) && len(pos) < 2; i++ {
		a := args[i]
		switch {
		case a == "-R" || a == "--repo" || a == "--hostname":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) == 2 && pos[0] == "pr" && pos[1] == "merge" {
		g.deny("workers never merge: gh pr merge is the orchestrator's seat")
	}
	if len(pos) >= 1 && pos[0] == "api" {
		g.ghAPI(args)
		if g.reason != "" {
			return nil
		}
		return g.ghGraphQL(args)
	}
	return nil
}

// ghAPI denies a write-method gh api call whose endpoint ends in /merge (the
// REST route behind gh pr merge). gh api defaults to GET, or POST once a body
// field is given.
func (g *guard) ghAPI(args []string) {
	method, endpoint, fields := "", "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-X" || a == "--method":
			if i+1 < len(args) {
				method = strings.ToUpper(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--method="):
			method = strings.ToUpper(strings.TrimPrefix(a, "--method="))
		case strings.HasPrefix(a, "-X") && len(a) > 2:
			method = strings.ToUpper(a[2:])
		case a == "--input" || ghSplitField(a):
			fields = true
			i++
		case ghJoinedField(a) != "" || strings.HasPrefix(a, "--input="):
			fields = true
		case a == "-R" || a == "--repo" || a == "--hostname" || a == "-H" || a == "--header" || a == "-q" || a == "--jq" || a == "-t" || a == "--template" || a == "--cache":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			if a != "api" && endpoint == "" {
				endpoint = a
			}
		}
	}
	if method == "" && fields {
		method = "POST"
	}
	if method == "" || method == "GET" || method == "HEAD" {
		return
	}
	path, _, _ := strings.Cut(endpoint, "?")
	if strings.HasSuffix(strings.TrimRight(path, "/"), "/merge") {
		g.deny("workers never merge: gh api %s %s is the orchestrator's seat", method, path)
	}
}

// ghGraphQL denies a gh api graphql call that carries a merge mutation: the
// same seat as gh pr merge (#3531). Queries and other mutations pass.
// The endpoint is not parsed for the mutation scan: any gh api argument naming
// a merge mutation is denied. A graphql query the guard cannot read (a @file,
// --input, or a value from a variable or substitution) fails closed (#3580).
func (g *guard) ghGraphQL(args []string) error {
	// Scanned on every gh api call, whatever spelling the endpoint takes
	// (/graphql, the full URL, a trailing slash).
	graphql := false
	for _, a := range args {
		for _, m := range []string{"mergePullRequest", "enablePullRequestAutoMerge"} {
			if strings.Contains(a, m) {
				g.deny("workers never merge: gh api graphql %s is the orchestrator's seat", m)
				return nil
			}
		}
		if !strings.HasPrefix(a, "-") && strings.HasSuffix(strings.TrimRight(a, "/"), "graphql") {
			graphql = true
		}
	}
	if !graphql {
		return nil
	}
	for i, a := range args {
		if a == "--input" || strings.HasPrefix(a, "--input=") {
			return guardErr("gh api graphql --input reads a query the guard cannot see")
		}
		field := ghJoinedField(a)
		if field == "" && i > 0 {
			if ghSplitField(args[i-1]) {
				field = a
			}
		}
		if field == "" {
			continue
		}
		a = field
		if v, ok := strings.CutPrefix(a, "query="); ok && (v == "" || strings.ContainsAny(v[:1], "@$`")) {
			return guardErr("gh api graphql query " + a + " is not a literal")
		}
	}
	return nil
}

// ghJoinedField returns the key=value of a gh field flag written as one word
// (--field=k=v, --raw-field=k=v, -Fk=v, -fk=v), or "" for any other word.
func ghJoinedField(a string) string {
	v, _ := ghFieldWord(a)
	return v
}

// ghFieldWord classifies a gh field flag word: joined is its key=value when
// written as one word; split reports a flag whose value is the next word
// (-f, -F, --field, --raw-field, or an -i cluster ending in f/F like -if).
func ghFieldWord(a string) (joined string, split bool) {
	if a == "--field" || a == "--raw-field" {
		return "", true
	}
	for _, p := range []string{"--field=", "--raw-field="} {
		if v, ok := strings.CutPrefix(a, p); ok && v != "" {
			return v, false
		}
	}
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return "", false
	}
	// A shorthand cluster: skip the boolean -i, then f/F takes the rest of the
	// word as its value (pflag strips one leading '=').
	for i := 1; i < len(a); i++ {
		switch a[i] {
		case 'i':
		case 'f', 'F':
			if i == len(a)-1 {
				return "", true
			}
			return strings.TrimPrefix(a[i+1:], "="), false
		default:
			return "", false
		}
	}
	return "", false
}

func ghSplitField(a string) bool {
	_, split := ghFieldWord(a)
	return split
}
