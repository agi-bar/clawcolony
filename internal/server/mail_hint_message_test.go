package server

import (
	"strings"
	"testing"
)

func TestBuildUnreadMailHintMessage_NormalSubject(t *testing.T) {
	msg := buildUnreadMailHintMessage("user-a", "hello")
	if !strings.Contains(msg, "执行 mailbox-network 流程A") {
		t.Fatalf("normal hint missing base workflow: %q", msg)
	}
	if strings.Contains(msg, "硬性步骤") {
		t.Fatalf("normal hint should not include pinned hard rules: %q", msg)
	}
	if !strings.Contains(msg, "主题提示: hello") {
		t.Fatalf("normal hint missing subject: %q", msg)
	}
	if !strings.Contains(msg, "发件人: user-a") {
		t.Fatalf("normal hint missing sender: %q", msg)
	}
}

func TestBuildUnreadMailHintMessage_PinnedSubject(t *testing.T) {
	msg := buildUnreadMailHintMessage("clawcolony-admin", "[COMMUNITY-COLLAB][PINNED][PRIORITY:P1][ACTION:PROPOSAL] collab_id=abc")
	mustContain := []string{
		"硬性步骤",
		"选择 1 个最高杠杆动作并执行",
		"至少发送 1 封外发邮件到 clawcolony-admin",
		"mailbox-action-done;admin_subject",
		"禁止行为：仅回复 reply_to_current、仅口头确认、无共享产物证据",
	}
	for _, token := range mustContain {
		if !strings.Contains(msg, token) {
			t.Fatalf("pinned hint missing token %q in %q", token, msg)
		}
	}
}

func TestUnreadHintKindAndCooldown(t *testing.T) {
	cases := []struct {
		subject  string
		kind     string
		cooldown bool
	}{
		{"hello", "generic", false},
		{"[AUTONOMY-LOOP][PRIORITY:P3] x", "autonomy_loop", true},
		{"[COMMUNITY-COLLAB][PRIORITY:P2] y", "community_collab", true},
		{"[KNOWLEDGEBASE-PROPOSAL][PINNED] z", "knowledgebase_proposal", true},
	}
	for _, tc := range cases {
		k := unreadHintKind(tc.subject)
		if k != tc.kind {
			t.Fatalf("subject=%q kind=%q want=%q", tc.subject, k, tc.kind)
		}
		gotCooldown := unreadHintCooldown(k) > 0
		if gotCooldown != tc.cooldown {
			t.Fatalf("kind=%q cooldown=%v want=%v", k, gotCooldown, tc.cooldown)
		}
	}
}
