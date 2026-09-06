package adapter

import (
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/publication"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// PublicationPlan preserves Claude's branch names, labels and retention selection.
// Config history intentionally excludes only cli/projects, not Desktop sessions.
func PublicationPlan(machine string, retention config.Retention) publication.Plan {
	return publication.Plan{
		RemoteName: "origin", Branch: "main", SnapshotMessage: "clauderig sync: " + machine, PushRetries: 3,
		History:   &publication.HistoryPlan{Branch: "config-history", Paths: []string{".", ":!cli/projects"}, CommitMessage: "clauderig config: " + machine, SquashMessage: "clauderig: squashed config history", MaxCommits: 200},
		Retention: publication.Retention{FloorBytes: retention.FloorBytes, SquashFactor: retention.SquashFactor, KeepDays: retention.KeepDays(), FoldMessage: func(cutoff time.Time) string { return "clauderig: history before " + cutoff.Format("2006-01-02") }},
	}
}
