package memory

import (
	"fmt"
	"testing"

	"github.com/rainea/nexus/pkg/types"
)

func TestConversationMemoryAddGetRecent(t *testing.T) {
	cm := NewConversationMemory(3)
	for i := 0; i < 5; i++ {
		cm.Add(types.Message{Role: types.RoleUser, Content: fmt.Sprintf("m%d", i)})
	}

	recent := cm.GetRecent(3)
	if len(recent) != 3 {
		t.Fatalf("GetRecent(3) = %d messages, want 3", len(recent))
	}
	if recent[0].Content != "m2" || recent[2].Content != "m4" {
		t.Fatalf("recent window wrong: %v", recent)
	}

	if all := cm.GetAll(); len(all) != 5 {
		t.Fatalf("GetAll = %d messages, want 5", len(all))
	}

	if cm.GetRecent(0) != nil {
		t.Fatalf("GetRecent(0) should return nil")
	}
}

func TestConversationMemoryCompact(t *testing.T) {
	cm := NewConversationMemory(2)
	for i := 0; i < 5; i++ {
		cm.Add(types.Message{Role: types.RoleUser, Content: fmt.Sprintf("m%d", i)})
	}
	cm.Compact(func(msgs []types.Message) string {
		if len(msgs) != 3 {
			t.Fatalf("summarizer got %d messages, want 3", len(msgs))
		}
		return "summary"
	})

	if got := len(cm.GetAll()); got != 2 {
		t.Fatalf("after compact got %d messages, want 2", got)
	}
	sums := cm.Summaries()
	if len(sums) != 1 || sums[0] != "summary" {
		t.Fatalf("summaries = %v, want [summary]", sums)
	}
}

func TestConversationMemoryClear(t *testing.T) {
	cm := NewConversationMemory(2)
	cm.Add(types.Message{Role: types.RoleUser, Content: "x"})
	cm.Clear()
	if got := len(cm.GetAll()); got != 0 {
		t.Fatalf("after clear got %d messages, want 0", got)
	}
	if got := len(cm.Summaries()); got != 0 {
		t.Fatalf("after clear got %d summaries, want 0", got)
	}
}
