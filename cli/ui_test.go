package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func testModel(t *testing.T) model {
	t.Helper()
	t.Setenv("AGENT_EVENT_LOG", filepath.Join(t.TempDir(), "events.jsonl"))
	return initialModel("127.0.0.1:0", "test-agent", "tok", "", t.TempDir(), ProviderConfig{})
}

func TestRiskyCommandRequiresApproval(t *testing.T) {
	m := testModel(t)
	updated, _ := m.Update(commandMsg{cmdStr: "rm -rf /"})
	if updated.(model).pendingApproval.requestID == "" {
		t.Fatal("a risky command should open a pending approval")
	}
}

func TestSafeCommandNeedsNoApproval(t *testing.T) {
	m := testModel(t)
	updated, _ := m.Update(commandMsg{cmdStr: "ls -la"})
	if updated.(model).pendingApproval.requestID != "" {
		t.Fatal("a safe command should not need approval")
	}
}

func TestBusyWhileApprovalPending(t *testing.T) {
	m := testModel(t)
	m.pendingApproval = pendingApproval{requestID: "abc", cmdStr: "rm -rf /"}

	updated, _ := m.Update(commandMsg{cmdStr: "ls"})
	got := updated.(model)
	if got.pendingApproval.requestID != "abc" {
		t.Fatal("the first pending approval must be kept")
	}
}

func TestApprovalDeniedClearsPending(t *testing.T) {
	m := testModel(t)
	m.pendingApproval = pendingApproval{requestID: "abc", cmdStr: "rm -rf /"}

	updated, _ := m.Update(approvalResponseMsg{requestID: "abc", approved: false})
	if updated.(model).pendingApproval.requestID != "" {
		t.Fatal("denying should clear the pending approval")
	}
}

func TestApprovalTimeoutClearsPending(t *testing.T) {
	m := testModel(t)
	m.pendingApproval = pendingApproval{requestID: "abc", cmdStr: "rm -rf /"}

	updated, _ := m.Update(approvalTimeoutMsg{requestID: "abc"})
	if updated.(model).pendingApproval.requestID != "" {
		t.Fatal("timeout should clear the pending approval")
	}
}

func TestListenEnvelopeParsesCommand(t *testing.T) {
	var env wsEnvelope
	if err := json.Unmarshal([]byte(`{"type":"command","command":"ls"}`), &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "command" || env.Command != "ls" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}
