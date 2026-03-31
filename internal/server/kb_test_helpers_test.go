package server

import "strings"

func kbProposalLongContent(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		seed = "runtime knowledge proposal regression coverage"
	}
	paragraph := seed + ". This regression fixture intentionally exceeds the anti-spam minimum by describing context, expected behavior, implementation notes, verification steps, rollback expectations, and reviewer guidance for a substantive runtime knowledge-base proposal body. "
	return strings.Repeat(paragraph, 4)
}
