package plan

import (
	"fmt"
	"regexp"
	"strconv"
)

var sliceUnitRe = regexp.MustCompile(`^S(\d+)`)

// nextUnit is the slice unit after the existing ones: S<NN> with NN one past
// the highest. Names that do not start with S<digits> are ignored.
func nextUnit(existing []string) string {
	max := 0
	for _, u := range existing {
		if m := sliceUnitRe.FindStringSubmatch(u); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("S%02d", max+1)
}
