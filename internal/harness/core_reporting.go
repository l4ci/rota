package harness

// coreReporter is optional: a worker adapter declares Core support only after
// native reporting and its composer/delivery behavior have been verified.
// Undeclared harnesses remain unsupported rather than inheriting readiness.
type coreReporter interface {
	CoreStatusApp() string
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
