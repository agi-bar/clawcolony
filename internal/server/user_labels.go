package server

import (
	"regexp"
	"strings"
)

// deployment-backed pod names look like <deployment>-<replicaset-hash>-<suffix>.
var deployPodNameRE = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]*[a-z0-9])?)-([a-z0-9]{9,10})-([a-z0-9]{5})$`)

func resolveUserIDFromLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	keys := []string{
		// Priority matters during migration: runtime-native label first, then legacy keys.
		"clawcolony.user_id",
		"landlord.bot_id",
		"landlord.user_id",
		"user_id",
		"bot_id",
	}
	for _, key := range keys {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}
	return ""
}

func deploymentNameFromPodName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	m := deployPodNameRE.FindStringSubmatch(name)
	if len(m) != 5 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func normalizeUserIDFromWorkloadName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Only unwrap known legacy deployment naming styles.
	// Do not strip generic "bot-*" names to avoid corrupting real user ids.
	if strings.HasPrefix(name, "bot-user-") || strings.HasPrefix(name, "bot-bot-") {
		return strings.TrimSpace(strings.TrimPrefix(name, "bot-"))
	}
	return name
}

func resolveUserIDFromWorkload(name string, labels map[string]string) string {
	if uid := resolveUserIDFromLabels(labels); uid != "" {
		return uid
	}
	workload := strings.TrimSpace(name)
	if dep := deploymentNameFromPodName(workload); dep != "" {
		workload = dep
	}
	return normalizeUserIDFromWorkloadName(workload)
}

func workloadMatchesUserID(name string, labels map[string]string, userID string) bool {
	target := strings.TrimSpace(userID)
	if target == "" {
		return false
	}
	if fromLabels := strings.TrimSpace(resolveUserIDFromLabels(labels)); fromLabels != "" {
		return fromLabels == target
	}
	workload := strings.TrimSpace(name)
	if workload == "" {
		return false
	}
	if workload == target || normalizeUserIDFromWorkloadName(workload) == target {
		return true
	}
	if dep := deploymentNameFromPodName(workload); dep != "" {
		if dep == target || normalizeUserIDFromWorkloadName(dep) == target {
			return true
		}
	}
	return false
}
