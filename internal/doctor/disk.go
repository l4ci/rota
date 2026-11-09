package doctor

import (
	"fmt"
	"strings"
)

// Disk is the space on one volume, in bytes.
type Disk struct {
	Path        string
	Free, Total uint64
}

// FreePercent is the free share of the volume, 0 when its size is unknown.
func (d *Disk) FreePercent() float64 {
	if d == nil || d.Total == 0 {
		return 0
	}
	return float64(d.Free) / float64(d.Total) * 100
}

// Low says whether the free share is under min percent. Unreadable disks and
// min <= 0 (the check is off) are never low; exactly min percent is not low.
func (d *Disk) Low(min int) bool {
	return d != nil && d.Total > 0 && min > 0 && d.FreePercent() < float64(min)
}

// disk warns when the free share of the volume is under the threshold, and
// names the rota leftovers that would give space back. It reports nothing when
// the space is fine, unreadable or the check is off.
func (d *runner) disk() (Check, bool) {
	disk, min := d.in.Disk, d.in.MinFreeDiskPercent
	if !disk.Low(min) {
		return Check{}, false
	}
	pct := disk.FreePercent()
	detail := fmt.Sprintf("%s has %s free (%.0f%%), under the %d%% threshold", disk.Path, HumanBytes(disk.Free), pct, min)
	hint := "free space before a round; set doctor.minFreeDiskPercent to change the threshold"
	if d.in.ScanLeftovers != nil {
		if left := d.in.ScanLeftovers(); len(left) > 0 {
			hint = "reclaimable rota leftovers: " + strings.Join(left, "; ")
		}
	}
	return Check{Name: "disk", Status: Warn, Detail: detail, Hint: hint}, true
}

// cacheDisk warns when the volume holding the Go build cache is under the same
// threshold: a full cache volume fails a gate with "no space left on device".
func (d *runner) cacheDisk() (Check, bool) {
	disk, min := d.in.CacheDisk, d.in.MinFreeDiskPercent
	if !disk.Low(min) {
		return Check{}, false
	}
	pct := disk.FreePercent()
	return Check{Name: "go-cache-disk", Status: Warn,
		Detail: fmt.Sprintf("%s (the Go build cache) has %s free (%.0f%%), under the %d%% threshold", disk.Path, HumanBytes(disk.Free), pct, min),
		Hint:   "run: go clean -cache; set doctor.minFreeDiskPercent to change the threshold"}, true
}

// HumanBytes is a size in the largest unit that keeps it above 1.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
