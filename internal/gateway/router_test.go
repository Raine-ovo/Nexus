package gateway

import "testing"

func TestComputeTier(t *testing.T) {
	cases := []struct {
		ch, usr string
		want    int
	}{
		{"slack", "u1", 1},
		{"slack", "*", 2},
		{"slack", "", 3},
		{"*", "u1", 4},
		{"*", "*", 5},
	}
	for _, tc := range cases {
		if g := computeTier(tc.ch, tc.usr); g != tc.want {
			t.Fatalf("computeTier(%q,%q)=%d want %d", tc.ch, tc.usr, g, tc.want)
		}
	}
}

func TestBindingRouter_Route(t *testing.T) {
	r := NewBindingRouter()
	r.AddBinding(Binding{Channel: "*", User: "*", AgentID: "default", Priority: 0})
	r.AddBinding(Binding{Channel: "c1", User: "", AgentID: "chanonly", Priority: 1})
	r.AddBinding(Binding{Channel: "c1", User: "alice", AgentID: "alice", Priority: 2})

	if id, ok := r.Route("c1", "alice"); !ok || id != "alice" {
		t.Fatalf("specific user: got %q %v", id, ok)
	}
	if id, ok := r.Route("c1", "bob"); !ok || id != "chanonly" {
		t.Fatalf("channel fallback: got %q %v", id, ok)
	}
	if id, ok := r.Route("other", "x"); !ok || id != "default" {
		t.Fatalf("catch-all: got %q %v", id, ok)
	}
}
