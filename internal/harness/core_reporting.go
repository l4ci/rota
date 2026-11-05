package harness

// coreReporter is optional: a worker adapter declares Core support only after
// native reporting and its composer/delivery behavior have been verified.
// Undeclared harnesses remain unsupported rather than inheriting readiness.
type coreReporter interface {
	CoreStatusApp() string
}

// coreComposerOptOut is optional: a harness whose composer rota does not
// verify before a Core send declares so. Without it the host verifies an
// empty, recognized composer first (the safe default, and Codex's behavior).
// A native blocked status and a visible dialog still refuse the send.
type coreComposerOptOut interface {
	CoreSkipsComposerCheck() bool
}

// CoreComposerChecked reports whether the Core host must verify an empty
// composer before sending to the worker identified by its OSC 7501 app. An
// unknown app is checked.
func CoreComposerChecked(app string) bool {
	for _, kind := range CoreWorkerKinds() {
		h, _ := lookup(kind)
		if h.(coreReporter).CoreStatusApp() != app {
			continue
		}
		if o, ok := h.(coreComposerOptOut); ok && o.CoreSkipsComposerCheck() {
			return false
		}
	}
	return true
}

// CoreWorkerKinds lists registered workers with verified Core reporting.
// It is independent of the legacy Herdr integration and contains no versions.
func CoreWorkerKinds() []string {
	var out []string
	for _, kind := range Kinds() {
		h, _ := lookup(kind)
		if r, ok := h.(coreReporter); ok && r.CoreStatusApp() != "" {
			out = append(out, kind)
		}
	}
	return out
}

// CoreStatusSupported reports whether an OSC 7501 app identifies a supported
// worker. Native state still needs the host's readiness and delivery checks.
func CoreStatusSupported(app string) bool {
	if app == "" {
		return false
	}
	for _, kind := range CoreWorkerKinds() {
		h, _ := lookup(kind)
		if h.(coreReporter).CoreStatusApp() == app {
			return true
		}
	}
	return false
}

// CoreWorkerKind resolves a native reporting identity to its registered kind.
func CoreWorkerKind(app string) (string, bool) {
	for _, kind := range CoreWorkerKinds() {
		h, _ := lookup(kind)
		if h.(coreReporter).CoreStatusApp() == app {
			return kind, true
		}
	}
	return "", false
}
