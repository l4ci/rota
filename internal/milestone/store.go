package milestone

// Store is where milestones live. FileStore keeps them under .rota/milestones;
// the issue and umbrella adapters live in package backlog, which imports this
// package and so can satisfy Store without a cycle. Minting an ID is part of
// Add: file mode bumps a persistent counter, issue mode reads the highest
// native milestone, so a bare NextID would not mean one thing across stores.
type Store interface {
	// Add mints the next milestone ID and creates the milestone as planned.
	Add(title, summary string, depends []string) (id string, err error)
	// List is every milestone in ID order, with Ready set from its depends.
	List() ([]Entry, error)
	// Show is the stored milestone text.
	Show(id string) (string, error)
	// Put replaces the milestone text, which must carry frontmatter with
	// `id: <id>`. changed is false when the text is already in place.
	Put(id, text string) (changed bool, err error)
	// SetStatus moves the milestone to one of Statuses. changed is false when
	// it already has that status.
	SetStatus(id, status string) (changed bool, err error)
	// OnTracker reports that milestones live on an issue tracker, so the
	// MILESTONES.md index is derived from List alone (see IndexFrom).
	OnTracker() bool
}

// FileStore is the Store of file mode: detail files under root/.rota/milestones.
type FileStore struct{ Root string }

var _ Store = FileStore{}

// Add writes the detail file and appends the entry to MILESTONES.md.
func (s FileStore) Add(title, summary string, depends []string) (string, error) {
	return addFile(s.Root, title, summary, depends)
}

// List reads the detail files; see List.
func (s FileStore) List() ([]Entry, error) { return List(s.Root) }

// Show is the detail file, verbatim.
func (s FileStore) Show(id string) (string, error) { return Show(s.Root, id) }

// Put replaces the detail file; see Put.
func (s FileStore) Put(id, text string) (bool, error) { return Put(s.Root, id, text) }

// SetStatus changes the frontmatter status only. Reindex regenerates the
// overview afterwards, for every store alike.
func (s FileStore) SetStatus(id, status string) (bool, error) {
	return setStatus(s.Root, id, status)
}

// OnTracker is false: the detail files are the source of truth.
func (FileStore) OnTracker() bool { return false }

// ActiveIDs are the IDs of the active entries of list, in list order.
func ActiveIDs(list []Entry) []string {
	ids := []string{}
	for _, e := range list {
		if e.Status == "active" {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// Reindex regenerates the MILESTONES.md overview and the vision block from
// the store's milestones.
func Reindex(root string, s Store) (changed bool, err error) {
	items, err := s.List()
	if err != nil {
		return false, err
	}
	return IndexFrom(root, items, s.OnTracker())
}
