package observability

import "testing"

func TestCallbackHandler_RunMetricsAggregation(t *testing.T) {
	tr := NewTracer()
	mc := NewMetricsCollector()
	cb := NewCallbackHandler(tr, mc)

	cb.OnStart(CallbackEvent{Type: "on_start", RunLabel: "governance"})
	cb.OnLLMStart(CallbackEvent{Type: "on_llm_start", RunLabel: "governance"})
	cb.OnLLMEnd(CallbackEvent{Type: "on_llm_end", RunLabel: "governance"})
	cb.OnToolStart(CallbackEvent{Type: "on_tool_start", RunLabel: "governance"})
	cb.OnToolEnd(CallbackEvent{Type: "on_tool_end", RunLabel: "governance"})
	cb.OnEnd(CallbackEvent{Type: "on_end", RunLabel: "governance"})

	runSnapshot := cb.SnapshotForRun("governance")
	counters, _ := runSnapshot["counters"].(map[string]int64)
	if counters["callbacks_on_start"] != 1 {
		t.Fatalf("expected run start count=1, got %+v", counters)
	}
	if counters["callbacks_on_llm_start"] != 1 || counters["callbacks_on_llm_end"] != 1 {
		t.Fatalf("expected llm callback counters in run snapshot, got %+v", counters)
	}
	if counters["callbacks_on_tool_start"] != 1 || counters["callbacks_on_tool_end"] != 1 {
		t.Fatalf("expected tool callback counters in run snapshot, got %+v", counters)
	}
	if counters["runs_active"] != 0 {
		t.Fatalf("expected balanced runs_active=0, got %+v", counters)
	}

	global := mc.Snapshot()
	globalCounters, _ := global["counters"].(map[string]int64)
	if globalCounters["callbacks_on_start"] != 1 || globalCounters["callbacks_on_end"] != 1 {
		t.Fatalf("expected global counters to be updated too, got %+v", globalCounters)
	}
}
