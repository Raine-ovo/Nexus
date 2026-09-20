package approval

import (
	"testing"
	"time"
)

func TestManagerCreateAndDecide(t *testing.T) {
	m := NewManager(time.Minute)
	r := m.Create("s1", "write_file", map[string]interface{}{"path": "a.txt"}, "needs approval")
	if r.ID == "" || r.Status != StatusPending {
		t.Fatalf("create = %+v", r)
	}
	if m.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", m.PendingCount())
	}

	if err := m.Approve(r.ID, false); err != nil {
		t.Fatal(err)
	}
	got, ok := m.Get(r.ID)
	if !ok || got.Status != StatusApproved {
		t.Fatalf("after approve = %+v", got)
	}
}

func TestManagerOneTimeGrantConsumed(t *testing.T) {
	m := NewManager(time.Minute)
	r := m.Create("s1", "bash", map[string]interface{}{"command": "echo hi"}, "shell")
	if err := m.Approve(r.ID, false); err != nil {
		t.Fatal(err)
	}
	if !m.IsGranted("bash", map[string]interface{}{"command": "echo hi"}) {
		t.Fatalf("first call should be granted")
	}
	if m.IsGranted("bash", map[string]interface{}{"command": "echo hi"}) {
		t.Fatalf("one-time grant should be consumed")
	}
}

func TestManagerPersistentGrant(t *testing.T) {
	m := NewManager(time.Minute)
	r := m.Create("s1", "bash", map[string]interface{}{"command": "ls"}, "shell")
	if err := m.Approve(r.ID, true); err != nil {
		t.Fatal(err)
	}
	if !m.IsGranted("bash", map[string]interface{}{"command": "ls"}) {
		t.Fatalf("first persistent call should be granted")
	}
	if !m.IsGranted("bash", map[string]interface{}{"command": "ls"}) {
		t.Fatalf("persistent grant should remain")
	}
}

func TestManagerDeny(t *testing.T) {
	m := NewManager(time.Minute)
	r := m.Create("s1", "bash", nil, "x")
	if err := m.Deny(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(r.ID)
	if got.Status != StatusDenied {
		t.Fatalf("status = %s, want denied", got.Status)
	}
	if m.IsGranted("bash", nil) {
		t.Fatalf("denied request must not grant")
	}
}

func TestManagerExpiry(t *testing.T) {
	m := NewManager(time.Millisecond)
	m.Create("s1", "bash", nil, "x")
	time.Sleep(5 * time.Millisecond)
	if m.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0 after expiry", m.PendingCount())
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	a := map[string]interface{}{"b": 2, "a": 1}
	b := map[string]interface{}{"a": 1, "b": 2}
	if fingerprint("tool", a) != fingerprint("tool", b) {
		t.Fatalf("fingerprint should ignore map key order")
	}
	if fingerprint("tool", a) == fingerprint("other", a) {
		t.Fatalf("fingerprint should differ by tool name")
	}
}
