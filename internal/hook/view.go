package hook

// File is one Claude settings file, read.
type File struct {
	Scope         Scope
	Path          string
	Events        map[string]string // marked events, see MarkedEvents
	StatusLineCmd string
	HasStatusLine bool
	Exists        bool // false only for a missing file; an unreadable one exists with Err set
	Err           error
}

// View is the settings files a run looks at, read once: the project's two
// files in precedence order, then one user file per config dir. It owns which
// of them count where, so the scope rule lives beside Scope.
type View struct {
	Local, Shared File   // settings.local.json, settings.json
	Users         []File // one per userDirs entry, same order
}

// LoadView reads the project's files under projectRoot and the user file in
// each of userDirs (already expanded).
func LoadView(projectRoot string, userDirs []string) *View {
	v := &View{
		Local:  readFile(ScopeProjectLocal, projectRoot),
		Shared: readFile(ScopeProject, projectRoot),
	}
	for _, dir := range userDirs {
		v.Users = append(v.Users, readFile(ScopeUser, dir))
	}
	return v
}

func readFile(scope Scope, base string) File {
	f := File{Scope: scope, Path: SettingsPath(scope, base)}
	o, err := ReadSettings(f.Path)
	if err != nil {
		f.Err, f.Exists = err, true
		return f
	}
	if o == nil {
		return f
	}
	f.Exists = true
	f.Events = MarkedEvents(o)
	_, f.StatusLineCmd, f.HasStatusLine = StatusLine(o)
	return f
}

// Project is the project's files in precedence order.
func (v *View) Project() []File { return []File{v.Local, v.Shared} }

// InScope is every file a hook in this run may live in.
func (v *View) InScope() []File { return append(v.Project(), v.Users...) }

// Worktree is the files a worker worktree reads: settings.local.json is
// untracked, so it never gets there.
func (v *View) Worktree() []File { return append([]File{v.Shared}, v.Users...) }
