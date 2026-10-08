package knowledge

import "strings"

// resolveTopic maps one requested topic to the headings it names, lowercased,
// deduplicated, in the order names lists them: the heading equal to want
// (case-insensitive) alone when there is one, else every heading containing
// want as a substring. An empty want or no hit resolves to nothing.
func resolveTopic(want string, names []string) []string {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" {
		return nil
	}
	var partial []string
	seen := map[string]bool{}
	for _, n := range names {
		l := strings.ToLower(n)
		if l == w {
			return []string{l}
		}
		if strings.Contains(l, w) && !seen[l] {
			seen[l] = true
			partial = append(partial, l)
		}
	}
	return partial
}
