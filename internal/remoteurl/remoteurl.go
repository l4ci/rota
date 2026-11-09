// Package remoteurl is the one reader of a git remote URL: host, owner and name.
package remoteurl

import "strings"

// Parts is what a remote URL names.
type Parts struct {
	Host  string // lowercased hostname, no port or userinfo
	Owner string // everything between the host and the repo name (may hold slashes)
	Name  string // repo name, no .git suffix
}

// Parse reads a remote in any URL form. A part it cannot find is "".
func Parse(remote string) Parts {
	r := strings.TrimSpace(remote)
	var host, path string
	switch i := strings.Index(r, "://"); {
	case i >= 0:
		host, path, _ = strings.Cut(r[i+3:], "/")
	case strings.HasPrefix(r, "/") || strings.HasPrefix(r, "."):
		path = r // a local path names no host
	case strings.Contains(r, ":"):
		host, path, _ = strings.Cut(r, ":") // scp-like: [user@]host:path
	default:
		host, path, _ = strings.Cut(r, "/")
	}
	if j := strings.LastIndex(host, "@"); j >= 0 {
		host = host[j+1:]
	}
	host, _, _ = strings.Cut(host, ":") // port
	p := Parts{Host: strings.ToLower(host)}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		p.Owner, p.Name = path[:i], path[i+1:]
	} else {
		p.Name = path
	}
	return p
}
