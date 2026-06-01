package skilltag

import (
	"encoding/json"
	"fmt"
	"strings"
)

type TaskDecompositionChallenge struct {
	MinWorkItems     int      `json:"min_work_items"`
	RequiredKeywords []string `json:"required_keywords"`
}

type TaskDecompositionSubmission struct {
	WorkItems []TaskWorkItem `json:"work_items"`
}

type TaskWorkItem struct {
	ItemID             string   `json:"item_id"`
	Title              string   `json:"title"`
	DependsOn          []string `json:"depends_on"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

type TaskDecompositionResult struct {
	Score   int            `json:"score"`
	Pass    bool           `json:"pass"`
	Reasons []string       `json:"reasons"`
	Metrics map[string]int `json:"metrics"`
}

func EvaluateTaskDecompositionJSON(challengeJSON []byte, submissionJSON []byte, passScore int) (TaskDecompositionResult, error) {
	var challenge TaskDecompositionChallenge
	if err := json.Unmarshal(challengeJSON, &challenge); err != nil {
		return TaskDecompositionResult{}, fmt.Errorf("decode challenge: %w", err)
	}
	var submission TaskDecompositionSubmission
	if err := json.Unmarshal(submissionJSON, &submission); err != nil {
		return TaskDecompositionResult{}, fmt.Errorf("decode submission: %w", err)
	}
	return EvaluateTaskDecomposition(challenge, submission, passScore), nil
}

func EvaluateTaskDecomposition(challenge TaskDecompositionChallenge, submission TaskDecompositionSubmission, passScore int) TaskDecompositionResult {
	if challenge.MinWorkItems <= 0 {
		challenge.MinWorkItems = 3
	}
	if passScore <= 0 {
		passScore = 80
	}

	reasons := make([]string, 0, 8)
	metrics := map[string]int{
		"count_score":      0,
		"dependency_score": 0,
		"acceptance_score": 0,
		"keyword_score":    0,
	}

	if len(submission.WorkItems) == 0 {
		return TaskDecompositionResult{
			Score:   0,
			Pass:    false,
			Reasons: []string{"work_items is empty"},
			Metrics: metrics,
		}
	}

	idSet := make(map[string]struct{}, len(submission.WorkItems))
	nonEmptyAcceptance := 0
	textBuf := strings.Builder{}
	hasDependencyEdge := false
	adj := make(map[string][]string, len(submission.WorkItems))

	for i, item := range submission.WorkItems {
		itemID := strings.TrimSpace(item.ItemID)
		title := strings.TrimSpace(item.Title)
		if itemID == "" {
			itemID = fmt.Sprintf("__idx_%d", i)
			reasons = append(reasons, "item_id is empty")
		}
		if _, exists := idSet[itemID]; exists {
			reasons = append(reasons, "duplicate item_id: "+itemID)
		}
		idSet[itemID] = struct{}{}
		if title == "" {
			reasons = append(reasons, "title is empty for item: "+itemID)
		}
		textBuf.WriteString(" ")
		textBuf.WriteString(title)
		if len(item.AcceptanceCriteria) > 0 {
			allNonEmpty := true
			for _, ac := range item.AcceptanceCriteria {
				ac = strings.TrimSpace(ac)
				if ac == "" {
					allNonEmpty = false
					continue
				}
				textBuf.WriteString(" ")
				textBuf.WriteString(ac)
			}
			if allNonEmpty {
				nonEmptyAcceptance++
			}
		}
		for _, dep := range item.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if dep == itemID {
				reasons = append(reasons, "self dependency: "+itemID)
				continue
			}
			adj[itemID] = append(adj[itemID], dep)
			hasDependencyEdge = true
		}
		if _, ok := adj[itemID]; !ok {
			adj[itemID] = nil
		}
	}

	hardFail := false

	// Count score (25)
	if len(submission.WorkItems) >= challenge.MinWorkItems {
		metrics["count_score"] = 25
	} else {
		metrics["count_score"] = (25 * len(submission.WorkItems)) / challenge.MinWorkItems
		reasons = append(reasons, fmt.Sprintf("work_items less than required: got=%d want>=%d", len(submission.WorkItems), challenge.MinWorkItems))
		hardFail = true
	}

	// Acceptance score (25)
	metrics["acceptance_score"] = (25 * nonEmptyAcceptance) / len(submission.WorkItems)
	if metrics["acceptance_score"] < 25 {
		reasons = append(reasons, "some work_items missing non-empty acceptance_criteria")
	}

	// Dependency score (25)
	dependencyOK := true
	for itemID, deps := range adj {
		for _, dep := range deps {
			if _, ok := idSet[dep]; !ok {
				dependencyOK = false
				reasons = append(reasons, fmt.Sprintf("depends_on unknown item_id: %s -> %s", itemID, dep))
			}
		}
	}
	cycle := hasCycle(adj)
	if dependencyOK && !cycle {
		if hasDependencyEdge {
			metrics["dependency_score"] = 25
		} else {
			metrics["dependency_score"] = 10
			reasons = append(reasons, "no dependency edges found")
		}
	} else {
		if cycle {
			reasons = append(reasons, "dependency graph has cycle")
		}
		metrics["dependency_score"] = 0
		hardFail = true
	}

	// Keyword score (25)
	if len(challenge.RequiredKeywords) == 0 {
		metrics["keyword_score"] = 25
	} else {
		text := strings.ToLower(textBuf.String())
		hit := 0
		for _, kw := range challenge.RequiredKeywords {
			kw = strings.ToLower(strings.TrimSpace(kw))
			if kw == "" {
				continue
			}
			if strings.Contains(text, kw) {
				hit++
			}
		}
		metrics["keyword_score"] = (25 * hit) / len(challenge.RequiredKeywords)
		if hit < len(challenge.RequiredKeywords) {
			reasons = append(reasons, fmt.Sprintf("keyword coverage insufficient: hit=%d total=%d", hit, len(challenge.RequiredKeywords)))
		}
	}

	score := metrics["count_score"] + metrics["dependency_score"] + metrics["acceptance_score"] + metrics["keyword_score"]
	pass := score >= passScore && !hardFail
	if !pass {
		reasons = append(reasons, fmt.Sprintf("score below pass threshold: got=%d pass_score=%d", score, passScore))
	}

	return TaskDecompositionResult{
		Score:   score,
		Pass:    pass,
		Reasons: dedupStrings(reasons),
		Metrics: metrics,
	}
}

func hasCycle(adj map[string][]string) bool {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(adj))
	var dfs func(string) bool
	dfs = func(node string) bool {
		switch state[node] {
		case visiting:
			return true
		case visited:
			return false
		}
		state[node] = visiting
		for _, nxt := range adj[node] {
			if dfs(nxt) {
				return true
			}
		}
		state[node] = visited
		return false
	}
	for node := range adj {
		if state[node] == unvisited {
			if dfs(node) {
				return true
			}
		}
	}
	return false
}

func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
