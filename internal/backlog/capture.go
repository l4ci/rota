package backlog

import (
	"regexp"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/pystr"
)

// Capture logic behind `rota item create` and `rota item shipped`: the input
// validation, the --raw-file bullet and the Depends on splice are file-ID
// grammar, so they live here and the verbs only read flags and files.

// ItemKinds are the kinds `item create` captures.
var ItemKinds = []string{"bugs", "features", "tasks"}

var itemSections = map[string]string{"bugs": "## Bugs", "features": "## Features", "tasks": "## Tasks"}

var (
	rawBulletID  = regexp.MustCompile(`\*\*\[(` + IDPattern(1) + `)\]`)
	dependsRefRe = regexp.MustCompile(`^(?:#\d+|` + IDPattern(FileIDDigits) + `)$`)
	newlineRepl  = strings.NewReplacer("\r\n", "\n", "\r", "\n")
)

// ParseDependsRefs splits a --depends-on value into item references, rejecting
// anything the readiness check could not look up.
func ParseDependsRefs(v string) ([]string, error) {
	var refs []string
	for _, t := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if !dependsRefRe.MatchString(t) {
			return nil, errf(ErrInvalid, "--depends-on %q is not an item reference (#N or an ID like B07)", t)
		}
		refs = append(refs, t)
	}
	if len(refs) == 0 {
		return nil, errf(ErrInvalid, "--depends-on needs a value")
	}
	return refs, nil
}

// CaptureRequest is what `item create --title` was given, flags already read.
type CaptureRequest struct {
	Kind, Title, Tag, Desc string
	// Fields are the named field flags (CreateFields, lower-cased) with their
	// values; Given marks the ones the caller passed, so an empty value that was
	// passed is refused rather than skipped.
	Fields map[string]string
	Given  map[string]bool
	Body   []byte
	// HasBody is true when a body was read.
	HasBody bool
	// DependsOn is the raw --depends-on value, used when DependsGiven.
	DependsOn    string
	DependsGiven bool
}

// ValidKind reports whether kind is one `item create` accepts.
func ValidKind(kind string) bool { return slices.Contains(ItemKinds, kind) }

// BuildCreateInput validates one capture and folds the fields and the Depends
// on section into a CreateInput. Bad input wraps ErrInvalid.
func BuildCreateInput(r CaptureRequest) (CreateInput, error) {
	if r.Title == "" {
		return CreateInput{}, errf(ErrInvalid, "--title is required")
	}
	in := CreateInput{Kind: r.Kind, Title: r.Title, Tag: r.Tag, Desc: r.Desc}
	for _, n := range CreateFields {
		key := strings.ToLower(n)
		if r.Given[key] && pystr.Strip(r.Fields[key]) == "" {
			return CreateInput{}, errf(ErrInvalid, "--%s needs a value", key)
		}
		if v := r.Fields[key]; v != "" {
			in.Fields = append(in.Fields, Field{Name: n, Value: v})
		}
	}
	in.Body, in.HasBody = r.Body, r.HasBody
	if r.DependsGiven {
		refs, err := ParseDependsRefs(r.DependsOn)
		if err != nil {
			return CreateInput{}, err
		}
		if itembody.HasDependsOn(in.Body) {
			return CreateInput{}, errf(ErrInvalid, "--depends-on conflicts with the ## Depends on section already in --body-file")
		}
		in.Body = itembody.AppendDependsOn(in.Body, refs)
		in.HasBody = true
	}
	return in, nil
}

// AppendRaw appends one preformatted bullet verbatim to kind's section and
// returns its ID. The bullet must carry a **[ID].
func AppendRaw(ops FileOps, kind string, raw []byte) (CreateResult, error) {
	entry := strings.TrimRight(newlineRepl.Replace(string(raw)), "\n")
	m := rawBulletID.FindStringSubmatch(entry)
	if m == nil {
		return CreateResult{}, errf(ErrInvalid, "--raw-file bullet needs a **[ID]")
	}
	if err := ops.Append(itemSections[kind], entry); err != nil {
		return CreateResult{}, err
	}
	typ, _ := TypeByKind(kind)
	return CreateResult{ID: m[1], Type: typ.Letter}, nil
}

// ShippedReport is the ship-evidence audit of some titles.
type ShippedReport struct {
	Audits []TitleAudit
	// Found is true when any title has a hit.
	Found bool
	// Text is the human rendering of the titles that have hits.
	Text string
}

// Shipped audits titles for ship evidence in the repo at dir; blank titles are
// skipped.
func Shipped(dir string, titles []string) *ShippedReport {
	rep := &ShippedReport{Audits: Audit(dir, titles)}
	var text strings.Builder
	for _, a := range rep.Audits {
		if len(a.Hits) == 0 {
			continue
		}
		rep.Found = true
		text.WriteString("=== " + a.Title + " ===\n")
		for _, h := range a.Hits {
			switch h.Level {
			case HitStrong:
				text.WriteString("  [STRONG] " + h.Hash + " " + h.Subject + "  (tokens: " + strings.Join(h.Tokens, ", ") + ")\n")
			case HitMedium:
				text.WriteString("  [MEDIUM] " + h.Hash + " " + h.Subject + "  (tokens: " + strings.Join(h.Tokens, ", ") + ")\n")
			default:
				text.WriteString("  [PATH]   " + h.Token + " → " + h.Path + "\n")
			}
		}
		text.WriteString("\n")
	}
	rep.Text = strings.TrimRight(text.String(), "\n")
	return rep
}
