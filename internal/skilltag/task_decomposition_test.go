package skilltag

import (
	"os"
	"testing"
)

func TestEvaluateTaskDecomposition_Pass(t *testing.T) {
	ch := TaskDecompositionChallenge{
		MinWorkItems:     4,
		RequiredKeywords: []string{"realtime", "scroll", "history", "test"},
	}
	sub := TaskDecompositionSubmission{
		WorkItems: []TaskWorkItem{
			{
				ItemID:             "W1",
				Title:              "接入 realtime stream",
				DependsOn:          nil,
				AcceptanceCriteria: []string{"message appears within 1s"},
			},
			{
				ItemID:             "W2",
				Title:              "修复 scroll lock",
				DependsOn:          []string{"W1"},
				AcceptanceCriteria: []string{"no auto jump while user reading history"},
			},
			{
				ItemID:             "W3",
				Title:              "补齐 history loading",
				DependsOn:          []string{"W2"},
				AcceptanceCriteria: []string{"older history can be fetched"},
			},
			{
				ItemID:             "W4",
				Title:              "新增 test cases",
				DependsOn:          []string{"W2", "W3"},
				AcceptanceCriteria: []string{"ui tests pass"},
			},
		},
	}

	got := EvaluateTaskDecomposition(ch, sub, 80)
	if !got.Pass {
		t.Fatalf("expected pass, got fail: score=%d reasons=%v", got.Score, got.Reasons)
	}
	if got.Score < 80 {
		t.Fatalf("expected score >= 80, got=%d", got.Score)
	}
}

func TestEvaluateTaskDecomposition_Fail_NotEnoughItems(t *testing.T) {
	ch := TaskDecompositionChallenge{
		MinWorkItems:     4,
		RequiredKeywords: []string{"realtime", "scroll", "history", "test"},
	}
	sub := TaskDecompositionSubmission{
		WorkItems: []TaskWorkItem{
			{
				ItemID:             "W1",
				Title:              "接入 realtime stream",
				DependsOn:          nil,
				AcceptanceCriteria: []string{"message appears within 1s"},
			},
			{
				ItemID:             "W2",
				Title:              "修复 scroll lock",
				DependsOn:          []string{"W1"},
				AcceptanceCriteria: []string{"no auto jump while reading history"},
			},
		},
	}

	got := EvaluateTaskDecomposition(ch, sub, 80)
	if got.Pass {
		t.Fatalf("expected fail, got pass: score=%d", got.Score)
	}
}

func TestEvaluateTaskDecomposition_Fail_DependencyCycle(t *testing.T) {
	ch := TaskDecompositionChallenge{
		MinWorkItems:     3,
		RequiredKeywords: []string{"api", "db"},
	}
	sub := TaskDecompositionSubmission{
		WorkItems: []TaskWorkItem{
			{
				ItemID:             "W1",
				Title:              "更新 api handler",
				DependsOn:          []string{"W3"},
				AcceptanceCriteria: []string{"handler compiles"},
			},
			{
				ItemID:             "W2",
				Title:              "更新 db store",
				DependsOn:          []string{"W1"},
				AcceptanceCriteria: []string{"store tests pass"},
			},
			{
				ItemID:             "W3",
				Title:              "补集成测试",
				DependsOn:          []string{"W2"},
				AcceptanceCriteria: []string{"integration tests pass"},
			},
		},
	}

	got := EvaluateTaskDecomposition(ch, sub, 80)
	if got.Pass {
		t.Fatalf("expected fail on cycle, got pass: score=%d", got.Score)
	}
	if got.Metrics["dependency_score"] != 0 {
		t.Fatalf("expected dependency_score=0, got=%d", got.Metrics["dependency_score"])
	}
}

func TestEvaluateTaskDecomposition_FromTestdataFiles(t *testing.T) {
	challengeRaw, err := os.ReadFile("testdata/task_decomposition/challenge_dashboard_chat.json")
	if err != nil {
		t.Fatalf("read challenge file: %v", err)
	}
	passRaw, err := os.ReadFile("testdata/task_decomposition/submission_pass.json")
	if err != nil {
		t.Fatalf("read pass submission file: %v", err)
	}
	failRaw, err := os.ReadFile("testdata/task_decomposition/submission_fail_cycle.json")
	if err != nil {
		t.Fatalf("read fail submission file: %v", err)
	}

	passRes, err := EvaluateTaskDecompositionJSON(challengeRaw, passRaw, 80)
	if err != nil {
		t.Fatalf("evaluate pass submission: %v", err)
	}
	if !passRes.Pass {
		t.Fatalf("expected pass submission to pass, score=%d reasons=%v", passRes.Score, passRes.Reasons)
	}

	failRes, err := EvaluateTaskDecompositionJSON(challengeRaw, failRaw, 80)
	if err != nil {
		t.Fatalf("evaluate fail submission: %v", err)
	}
	if failRes.Pass {
		t.Fatalf("expected fail submission to fail, score=%d", failRes.Score)
	}
}
