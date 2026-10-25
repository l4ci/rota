# Go

- **Table tests need a per-case name** — Pass the case name to `t.Run`, or a failing case prints only an index. <!-- 2026-10-12 source:rota -->
- **Cache the build on a big volume** — Point `GOCACHE` at a disk with room; a full cache volume fails a build with "no space left on device". <!-- 2026-09-30 source:billing-api -->
