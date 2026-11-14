package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"alfaos/alfad/internal/store"
)

// Commands on the host are a terminal: administrators only, whatever the
// agent's scopes say (a person could otherwise give their own agent
// terminal access and approve it themselves).
func TestAgentHostRunIsAdminOnly(t *testing.T) {
	s := &Server{}
	for _, u := range []*store.User{nil, {Role: store.RoleUser}} {
		if _, err := s.agentAccount(&agentBackend{u: u}); !errors.Is(err, errAgentHostAdminOnly) {
			t.Fatalf("non-admin got a host account: %v", err)
		}
	}
	if _, err := s.agentAccount(nil); !errors.Is(err, errAgentHostAdminOnly) {
		t.Fatalf("no backend: %v", err)
	}
	if !findAgentTool("run_command").admin {
		t.Fatal("run_command must be an administrators' tool")
	}
}

// An approved call runs with what the agent may do once approved.
func TestStillMayRunAfterApproval(t *testing.T) {
	tool := findAgentTool("write_file")
	admin := &store.User{Role: store.RoleAdmin}
	now := time.Now()
	ok := &store.AgentToken{Scope: store.AgentScopeAct}
	if !stillMayRun(tool, ok, admin, now) {
		t.Fatal("an unchanged agent may run")
	}
	paused := *ok
	paused.Paused = true
	expired := *ok
	expired.ExpiresAt = now.Add(-time.Second)
	narrowed := *ok
	narrowed.Scope = store.AgentScopeRead
	for name, tok := range map[string]*store.AgentToken{"paused": &paused, "expired": &expired, "narrowed": &narrowed, "gone": nil} {
		if stillMayRun(tool, tok, admin, now) {
			t.Errorf("%s agent still ran", name)
		}
	}
}

// What would run is never cut on the approval card.
func TestApprovalCardShowsCodeWhole(t *testing.T) {
	code := strings.Repeat("a", 3000) + "\nrm -rf ~"
	out := argsForCard(map[string]any{"code": code, "command": code, "note": code, "password": "x"})
	if out["code"] != code || out["command"] != code {
		t.Fatal("code or command was cut")
	}
	if s, _ := out["note"].(string); strings.Contains(s, "rm -rf") {
		t.Fatal("other long values are still shortened")
	}
	if out["password"] == "x" {
		t.Fatal("password shown")
	}
	huge := strings.Repeat("b", cardWholeMax+10)
	if s, _ := argsForCard(map[string]any{"content": huge})["content"].(string); !strings.Contains(s, "more characters") {
		t.Fatal("a very long file is cut and says so")
	}
}
