package plan

import "fmt"

// Limits are the advisory size thresholds of rota plan check; 0 turns one off.
type Limits struct {
	MaxTasks        int // plan.maxTasks
	MaxFilesPerTask int // plan.maxFilesPerTask
}

// CheckSize returns advisory warnings for a plan with more than MaxTasks
// top-level tasks and for each task whose Files: line names more than
// MaxFilesPerTask paths. Size never makes a plan unsound, so it stays apart
// from Report.Ok. Pure.
func CheckSize(body string, lim Limits) []string {
	var warns []string
	blocks := taskBlocks(body)
	if lim.MaxTasks > 0 && len(blocks) > lim.MaxTasks {
		warns = append(warns, fmt.Sprintf("plan has %d tasks (plan.maxTasks is %d): consider splitting it into dependent items", len(blocks), lim.MaxTasks))
	}
	if lim.MaxFilesPerTask > 0 {
		for _, b := range blocks {
			if n := len(fileTokens(b.lines)); n > lim.MaxFilesPerTask {
				warns = append(warns, fmt.Sprintf("%s names %d files (plan.maxFilesPerTask is %d): consider splitting it", b.name, n, lim.MaxFilesPerTask))
			}
		}
	}
	return warns
}
