package marker

import (
	"slices"
	"time"
)

// ClaimComment is one comment as the claim fold reads it.
type ClaimComment struct {
	Body string
	At   time.Time // zero when the forge did not say
}

// ClaimEvent is a claim or release marker found in a comment.
type ClaimEvent struct {
	Verb string // KindClaim or KindRelease
	ID   string
	Hand bool // a release a person chose to make
	At   time.Time
}

// ClaimState is what one pass over an issue's claim markers says.
type ClaimState struct {
	Events       []ClaimEvent         // every marker, in comment order
	Held         []string             // claim ids with no later release, in claim order
	ClaimedAt    map[string]time.Time // first claim time of each held id; nil when none
	HandReleased []string             // ids whose last marker is a hand release
}

// FoldClaims is the one reading of claim markers: held claims, when each was
// taken and which were handed back by a person. It is pure; callers that keep
// claims elsewhere (a tracker claim store) filter the result against it.
func FoldClaims(comments []ClaimComment) ClaimState {
	var st ClaimState
	for _, c := range comments {
		verb, id, ok := ParseClaim(c.Body)
		if !ok {
			continue
		}
		hand := verb == KindRelease && IsHandRelease(c.Body, id)
		st.Events = append(st.Events, ClaimEvent{Verb: verb, ID: id, Hand: hand, At: c.At})
		// Any later marker for an id supersedes an earlier hand release.
		st.HandReleased = slices.DeleteFunc(st.HandReleased, func(h string) bool { return h == id })
		if hand {
			st.HandReleased = append(st.HandReleased, id)
		}
		if verb == KindClaim {
			if !slices.Contains(st.Held, id) {
				st.Held = append(st.Held, id)
				if st.ClaimedAt == nil {
					st.ClaimedAt = map[string]time.Time{}
				}
				st.ClaimedAt[id] = c.At
			}
		} else if i := slices.Index(st.Held, id); i >= 0 {
			st.Held = slices.Delete(st.Held, i, i+1)
			delete(st.ClaimedAt, id)
		}
	}
	return st
}
