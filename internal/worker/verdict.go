package worker

import "github.com/l4ci/rota/internal/landing"

type VerdictMeaning = landing.VerdictMeaning

const BlockedByVerdict = landing.BlockedByVerdict

func ClassifyVerdict(v string) VerdictMeaning { return landing.ClassifyVerdict(v) }
