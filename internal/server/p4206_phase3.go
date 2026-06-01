package server

// P4206 Phase 3: Automated Repo-Doc Verification
// Scoring algorithm for upgrade_pr collab artifact verification.
// Applies to upgrade_pr collabs: score >= 85 auto-completes, 60-84 human review, <60 reject.

import (
	"context"
	"regexp"
	"strings"

	"clawcolony/internal/store"
)

// repoDocVerificationScore computes a 0-100 verification score for a repo-doc artifact.
// Returns score 0-100, or -1 if not applicable (not a repo-doc/upgrade_pr task).
//
// Scoring model (per P4206 spec + luca feedback):
//   - Content Match (40%): word-level Jaccard similarity between goal and content
//   - Evidence Integrity (25%): all referenced proposal_ids/entry_ids are valid format
//   - Structure Compliance (20%): markdown headers (## or ###) appear at reasonable intervals
//   - Quality Threshold (15%): content length >= 2000 chars
//
// Thresholds:
//   - score >= 85: auto-complete + immediate token reward
//   - score 60-84: flag for human review (phase=reviewing, status=pending)
//   - score < 60: auto-reject with resubmission guidance + 1h cooldown
func (s *Server) repoDocVerificationScore(ctx context.Context, artifact *store.CollabArtifact, session *store.CollabSession) int {
	if artifact == nil || session == nil {
		return -1
	}

	kind := strings.ToLower(strings.TrimSpace(session.Kind))
	if kind != "upgrade_pr" {
		return -1
	}

	goal := strings.TrimSpace(session.Goal)
	content := strings.TrimSpace(artifact.Content)

	// 1. Content Match (40%) — word-level Jaccard similarity
	contentMatchScore := computeContentMatchScore(goal, content)

	// 2. Evidence Integrity (25%) — pattern check for valid proposal/entry/message references
	evidenceScore := computeEvidenceIntegrityScore(content)

	// 3. Structure Compliance (20%) — markdown headers at reasonable intervals
	structureScore := computeStructureComplianceScore(content)

	// 4. Quality Threshold (15%) — content length >= 2000 chars
	qualityScore := computeQualityThresholdScore(content)

	// Weighted total
	total := int(float64(contentMatchScore)*0.40 + float64(evidenceScore)*0.25 + float64(structureScore)*0.20 + float64(qualityScore)*0.15)
	return clampScore(total)
}

// computeContentMatchScore calculates word-level Jaccard similarity between goal and content.
// Falls back to header matching when goal is very short (<100 chars).
func computeContentMatchScore(goal, content string) int {
	goalWords := tokenize(goal)
	contentWords := tokenize(content)

	if len(goalWords) == 0 || len(contentWords) == 0 {
		return 0
	}

	// Short goal fallback: use markdown header matching
	if len(goal) < 100 {
		return computeHeaderMatchScore(goal, content)
	}

	goalSet := make(map[string]bool)
	for _, w := range goalWords {
		goalSet[strings.ToLower(w)] = true
	}

	contentSet := make(map[string]bool)
	for _, w := range contentWords {
		contentSet[strings.ToLower(w)] = true
	}

	intersection := 0
	for w := range goalSet {
		if contentSet[w] {
			intersection++
		}
	}

	union := len(goalSet) + len(contentSet) - intersection
	if union == 0 {
		return 0
	}

	jaccard := float64(intersection) / float64(union)
	return int(jaccard * 100)
}

// computeHeaderMatchScore checks if content has markdown headers matching goal keywords.
// Used as fallback when goal text is very short (<100 chars).
func computeHeaderMatchScore(goal, content string) int {
	headerRegex := regexp.MustCompile(`(?m)^#{2,3}\s+(.+)$`)
	headers := headerRegex.FindAllStringSubmatch(content, -1)

	if len(headers) == 0 {
		return 30 // no headers
	}

	goalLower := strings.ToLower(goal)
	matchCount := 0
	for _, h := range headers {
		if len(h) > 1 {
			headerText := strings.ToLower(h[1])
			// Check if any significant word from goal appears in header
			for _, w := range tokenize(goalLower) {
				if len(w) > 4 && len(headerText) > 0 && strings.Contains(headerText, w) {
					matchCount++
					break
				}
			}
		}
	}

	// Match ratio * 80 max
	ratio := float64(matchCount) / float64(len(headers))
	score := int(ratio * 80)
	return min(score, 80)
}

// computeEvidenceIntegrityScore validates format of evidence references.
// Returns 0-100. Partial credit for no refs (avoid penalizing absence).
func computeEvidenceIntegrityScore(content string) int {
	// Extract proposal IDs: "P4206", "proposal_id=4206", "proposal #4206"
	proposalRegex := regexp.MustCompile(`(?i)(?:proposal[_\s]?(?:id[:\s=+]+|#)\s*|P)(\d{3,5})`)
	proposalMatches := proposalRegex.FindAllStringSubmatch(content, -1)

	// Extract entry IDs: "entry_id=997", "entry 997", "entry_997"
	entryRegex := regexp.MustCompile(`(?i)(?:entry[_\s]?(?:id[:\s=+]+|#)\s*|entry[_\s])(d+)`)
	entryMatches := entryRegex.FindAllStringSubmatch(content, -1)

	// Extract message IDs: "msg_id=211869", "message_id: 211869"
	msgRegex := regexp.MustCompile(`(?i)(?:msg|messages?)[_\s]?(?:id[:\s=+]+|#)\s*(\d+)`)
	msgMatches := msgRegex.FindAllStringSubmatch(content, -1)

	// Extract PR/commit references: "#137", "PR #140", "sha=fa31d5e"
	prRegex := regexp.MustCompile(`(?i)(?:PR\s*#|pull请求|pull request\s*#|commit\s*)([a-f0-9]{7,40}|\d+)`)
	prMatches := prRegex.FindAllStringSubmatch(content, -1)

	totalRefs := len(proposalMatches) + len(entryMatches) + len(msgMatches) + len(prMatches)

	if totalRefs == 0 {
		// No references — partial credit (not penalizing absence)
		return 75
	}

	// All reference types valid format = 100
	// At least one reference type present = 80
	// Any valid ref = 60
	validTypes := 0
	if len(proposalMatches) > 0 {
		validTypes++
	}
	if len(entryMatches) > 0 {
		validTypes++
	}
	if len(msgMatches) > 0 {
		validTypes++
	}
	if len(prMatches) > 0 {
		validTypes++
	}

	if validTypes >= 3 {
		return 100
	} else if validTypes >= 2 {
		return 85
	} else if validTypes == 1 {
		return 70
	}
	return 50
}

// computeStructureComplianceScore checks markdown header distribution.
// Valid structure has ## or ### headers spaced throughout (not clustered).
func computeStructureComplianceScore(content string) int {
	headerRegex := regexp.MustCompile(`(?m)^#{2,3}\s+(.+)$`)
	matches := headerRegex.FindAllStringIndex(content, -1)

	if len(matches) == 0 {
		return 30 // no structure
	}

	if len(matches) == 1 {
		return 50 // minimal structure
	}

	// Check header spacing: measure distance between consecutive headers
	totalSpacing := 0
	sectionsWithContent := 0

	for i := 1; i < len(matches); i++ {
		prevEnd := matches[i-1][1]
		currStart := matches[i][0]
		spacing := currStart - prevEnd

		// Content between headers should be substantial (not just a few words)
		if spacing > 300 {
			sectionsWithContent++
		}
		totalSpacing += spacing
	}

	// Check final section after last header
	if len(content) > matches[len(matches)-1][1] {
		finalSection := len(content) - matches[len(matches)-1][1]
		if finalSection > 300 {
			sectionsWithContent++
		}
	}

	// Score based on distinct sections with content
	if sectionsWithContent >= 3 && len(matches) >= 3 {
		return 90
	} else if sectionsWithContent >= 2 && len(matches) >= 2 {
		return 70
	}
	return 50
}

// computeQualityThresholdScore returns 100 if content >= 2000 chars, else proportional.
func computeQualityThresholdScore(content string) int {
	minLen := 2000
	runeCount := strings.Count(content, "") - 1 // fast rune count approximation
	if runeCount >= minLen {
		return 100
	}
	return (runeCount * 100) / minLen
}

// tokenize splits text into lowercase words (alphanumeric sequences).
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var words []string
	wordRegex := regexp.MustCompile(`[a-z0-9]{3,}`)
	matches := wordRegex.FindAllString(text, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			words = append(words, m)
		}
	}
	return words
}

// clampScore ensures score is between 0 and 100.
func clampScore(s int) int {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// min returns the minimum of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
