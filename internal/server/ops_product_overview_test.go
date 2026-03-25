package server

import (
	"context"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func TestBuildOpsProductOverviewAppliesWindowToKBHighlightsAndContributors(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	mustUpsertOpsBot(t, srv, "alice", "alice")
	mustUpsertOpsBot(t, srv, "bob", "bob")

	now := time.Now().UTC()
	oldAppliedAt := now.Add(-48 * time.Hour)
	recentAppliedAt := now.Add(-2 * time.Hour)

	mustCreateAppliedKBProposalAt(t, srv, ctx, "alice", "Old KB entry", oldAppliedAt)
	mustCreateAppliedKBProposalAt(t, srv, ctx, "bob", "Recent KB entry", recentAppliedAt)

	resp, err := srv.buildOpsProductOverview(ctx, now, now.Add(-24*time.Hour), now, opsProductWindow24h, false)
	if err != nil {
		t.Fatalf("build ops product overview: %v", err)
	}

	kbSection := mustFindOpsSection(t, resp, "kb")
	if got, want := kbSection.WindowOutput["kb_applied"], 1; got != want {
		t.Fatalf("kb window_output[kb_applied]=%d want=%d", got, want)
	}
	if len(kbSection.Highlights) != 1 {
		t.Fatalf("kb highlights len=%d want=1", len(kbSection.Highlights))
	}
	if got, want := kbSection.Highlights[0].Title, "Recent KB entry"; got != want {
		t.Fatalf("kb highlight title=%q want=%q", got, want)
	}
	if len(kbSection.TopContributors) != 1 {
		t.Fatalf("kb top contributors len=%d want=1", len(kbSection.TopContributors))
	}
	if got, want := kbSection.TopContributors[0].UserID, "bob"; got != want {
		t.Fatalf("kb top contributor user_id=%q want=%q", got, want)
	}
	if got, want := kbSection.TopContributors[0].Count, 1; got != want {
		t.Fatalf("kb top contributor count=%d want=%d", got, want)
	}

	globalKB := resp.TopContributors["kb"]
	if len(globalKB) != 1 || globalKB[0].UserID != "bob" {
		t.Fatalf("global kb contributors=%+v want only bob", globalKB)
	}
}

func TestBuildOpsProductOverviewAppliesWindowToToolHighlightsAndContributors(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	mustUpsertOpsBot(t, srv, "tool-old", "tool-old")
	mustUpsertOpsBot(t, srv, "tool-new", "tool-new")

	now := time.Now().UTC()
	oldAt := now.Add(-10 * 24 * time.Hour)
	recentAt := now.Add(-3 * time.Hour)

	if err := srv.saveToolRegistryState(ctx, toolRegistryState{
		Items: []toolRegistryItem{
			{
				ToolID:       "old-tool",
				Name:         "Old Tool",
				Tier:         "t1",
				AuthorUserID: "tool-old",
				Status:       "active",
				CreatedAt:    oldAt,
				UpdatedAt:    oldAt,
				ActivatedAt:  &oldAt,
			},
			{
				ToolID:       "recent-tool",
				Name:         "Recent Tool",
				Tier:         "t1",
				AuthorUserID: "tool-new",
				Status:       "active",
				CreatedAt:    recentAt,
				UpdatedAt:    recentAt,
				ActivatedAt:  &recentAt,
			},
		},
	}); err != nil {
		t.Fatalf("save tool registry state: %v", err)
	}

	resp, err := srv.buildOpsProductOverview(ctx, now, now.Add(-24*time.Hour), now, opsProductWindow24h, false)
	if err != nil {
		t.Fatalf("build ops product overview: %v", err)
	}

	toolsSection := mustFindOpsSection(t, resp, "tools")
	if got, want := toolsSection.WindowOutput["tools_activated"], 1; got != want {
		t.Fatalf("tools window_output[tools_activated]=%d want=%d", got, want)
	}
	if len(toolsSection.Highlights) != 1 {
		t.Fatalf("tools highlights len=%d want=1", len(toolsSection.Highlights))
	}
	if got, want := toolsSection.Highlights[0].Title, "Recent Tool"; got != want {
		t.Fatalf("tools highlight title=%q want=%q", got, want)
	}
	if len(toolsSection.TopContributors) != 1 {
		t.Fatalf("tools top contributors len=%d want=1", len(toolsSection.TopContributors))
	}
	if got, want := toolsSection.TopContributors[0].UserID, "tool-new"; got != want {
		t.Fatalf("tools top contributor user_id=%q want=%q", got, want)
	}
	if got, want := toolsSection.TopContributors[0].Count, 1; got != want {
		t.Fatalf("tools top contributor count=%d want=%d", got, want)
	}
}

func mustUpsertOpsBot(t *testing.T, srv *Server, userID, username string) {
	t.Helper()
	if _, err := srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       userID,
		Name:        username,
		Provider:    "test",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("upsert bot %s: %v", userID, err)
	}
}

func mustCreateAppliedKBProposalAt(t *testing.T, srv *Server, ctx context.Context, proposer, title string, appliedAt time.Time) {
	t.Helper()
	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID: proposer,
		Title:          title,
		Reason:         "ops overview window test",
		Status:         "discussing",
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "kb/test",
		Title:      title,
		NewContent: "test content",
		DiffText:   "+test content",
	})
	if err != nil {
		t.Fatalf("create kb proposal %q: %v", title, err)
	}
	if _, err := srv.store.CloseKBProposal(ctx, proposal.ID, "approved", "approved for test", 0, 0, 0, 0, 0, appliedAt); err != nil {
		t.Fatalf("close kb proposal %q: %v", title, err)
	}
	if _, _, err := srv.store.ApplyKBProposal(ctx, proposal.ID, proposer, appliedAt); err != nil {
		t.Fatalf("apply kb proposal %q: %v", title, err)
	}
}

func mustFindOpsSection(t *testing.T, resp opsProductOverviewResponse, module string) opsProductSection {
	t.Helper()
	for _, sec := range resp.Sections {
		if sec.Module == module {
			return sec
		}
	}
	t.Fatalf("section %q not found in %+v", module, resp.Sections)
	return opsProductSection{}
}
