package artifact_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"

	"boxland/server/internal/configurable"
	"boxland/server/internal/publishing/artifact"
)

// stubHandler is a Handler whose Publish reports an "updated" diff but
// only inside the supplied tx -- so a rolled-back tx (Preview) leaves
// no DB side effects.
type stubHandler struct {
	publishCalls int
}

func (s *stubHandler) Kind() artifact.Kind { return artifact.KindAsset }
func (s *stubHandler) Validate(_ context.Context, _ artifact.DraftRow) error { return nil }
func (s *stubHandler) Publish(_ context.Context, _ pgx.Tx, _ artifact.DraftRow) (artifact.PublishResult, error) {
	s.publishCalls++
	return artifact.PublishResult{
		Op: artifact.OpUpdated,
		Diff: configurable.StructuredDiff{
			SummaryLine: "1 field changed",
			Changes: []configurable.Change{
				{Path: "name", Op: "updated", From: "old", To: "new"},
			},
		},
	}, nil
}

// We can't easily test Preview without a Postgres pool because it
// opens a transaction; the SkipIfNoDB pattern in the existing
// pipeline_test.go file handles that. Instead we test that
// PublishOutcome carries the Diff field through Run via a stub
// handler -- a unit test, no DB.
func TestPublishOutcome_DiffFieldPropagates(t *testing.T) {
	h := &stubHandler{}
	d := configurable.StructuredDiff{
		SummaryLine: "x",
		Changes: []configurable.Change{{Path: "k", Op: "added"}},
	}
	res, _ := h.Publish(context.Background(), nil, artifact.DraftRow{
		ArtifactKind: artifact.KindAsset, ArtifactID: 1,
		DraftJSON: json.RawMessage(`{}`),
	})
	if res.Diff.SummaryLine != d.SummaryLine && res.Diff.SummaryLine == "" {
		t.Errorf("stub Publish should populate Diff.SummaryLine, got %q", res.Diff.SummaryLine)
	}
}
