package gateway

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/rainea/nexus/internal/observability"
)

func (g *Gateway) handleDebugMetrics(w http.ResponseWriter, r *http.Request) {
	dbg, ok := g.observer.(debugObserver)
	if !ok {
		http.Error(w, "debug metrics unavailable", http.StatusNotImplemented)
		return
	}
	run := strings.TrimSpace(r.URL.Query().Get("run"))
	metrics := dbg.MetricsSnapshot()
	scope := "global"
	if run != "" {
		metrics = dbg.MetricsSnapshotForRun(run)
		scope = "run"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"run":     run,
		"scope":   scope,
		"metrics": metrics,
	})
}

func (g *Gateway) handleDebugScopes(w http.ResponseWriter, r *http.Request) {
	dbg, ok := g.supervisor.(ScopeDebugger)
	if !ok {
		http.Error(w, "debug scopes unavailable", http.StatusNotImplemented)
		return
	}
	scopes := dbg.DebugScopes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count":  len(scopes),
		"scopes": scopes,
	})
}

func (g *Gateway) handleDebugTraces(w http.ResponseWriter, r *http.Request) {
	dbg, ok := g.observer.(debugObserver)
	if !ok {
		http.Error(w, "debug traces unavailable", http.StatusNotImplemented)
		return
	}
	run := strings.TrimSpace(r.URL.Query().Get("run"))
	traces := filterTraceSummariesByRun(dbg.ListTraces(100), run)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"run":    run,
		"count":  len(traces),
		"traces": traces,
	})
}

func (g *Gateway) handleDebugTrace(w http.ResponseWriter, r *http.Request) {
	dbg, ok := g.observer.(debugObserver)
	if !ok {
		http.Error(w, "debug traces unavailable", http.StatusNotImplemented)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "trace id required"})
		return
	}
	trace := dbg.Trace(id)
	if len(trace) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown trace"})
		return
	}
	run := strings.TrimSpace(r.URL.Query().Get("run"))
	if run != "" && traceRunLabel(trace) != run {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "trace not found for run"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	grouped := summarizeTraceDurations(trace)
	scopeSummary := extractScopeSummary(trace)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"trace_id":      id,
		"run":           run,
		"spans":         trace,
		"tree":          buildTraceTree(trace),
		"grouped":       grouped,
		"errors":        grouped["errors"],
		"scope_summary": scopeSummary,
	})
}

func (g *Gateway) handleDebugDashboard(w http.ResponseWriter, r *http.Request) {
	dbg, ok := g.observer.(debugObserver)
	if !ok {
		http.Error(w, "debug dashboard unavailable", http.StatusNotImplemented)
		return
	}
	run := strings.TrimSpace(r.URL.Query().Get("run"))
	data := struct {
		Run        string
		TraceCount int
		ScopesURL  string
		MetricsURL string
		TracesURL  string
		HealthURL  string
	}{
		Run:        run,
		TraceCount: len(filterTraceSummariesByRun(dbg.ListTraces(100), run)),
		ScopesURL:  "/api/debug/scopes",
		MetricsURL: "/api/debug/metrics?run=" + template.URLQueryEscaper(run),
		TracesURL:  "/api/debug/traces?run=" + template.URLQueryEscaper(run),
		HealthURL:  "/api/health",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = debugDashboardTemplate.Execute(w, data)
}

func filterTraceSummariesByRun(in []observability.TraceSummary, run string) []observability.TraceSummary {
	run = strings.TrimSpace(run)
	if run == "" {
		return in
	}
	out := make([]observability.TraceSummary, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(item.RunLabel) == run {
			out = append(out, item)
		}
	}
	return out
}

func buildTraceTree(spans []*observability.Span) []traceTreeNode {
	nodes := make(map[string]*traceTreeNode, len(spans))
	roots := make([]*traceTreeNode, 0)
	for _, span := range spans {
		if span == nil {
			continue
		}
		nodes[span.SpanID] = &traceTreeNode{Span: span}
	}
	for _, span := range spans {
		if span == nil {
			continue
		}
		node := nodes[span.SpanID]
		if parent := nodes[span.ParentID]; parent != nil {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Span.StartTime.Before(roots[j].Span.StartTime)
	})
	for _, root := range roots {
		sortTraceChildren(root)
	}
	out := make([]traceTreeNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, *root)
	}
	return out
}

func sortTraceChildren(node *traceTreeNode) {
	sort.Slice(node.Children, func(i, j int) bool {
		return node.Children[i].Span.StartTime.Before(node.Children[j].Span.StartTime)
	})
	for _, child := range node.Children {
		sortTraceChildren(child)
	}
}

func summarizeTraceDurations(spans []*observability.Span) map[string]interface{} {
	type bucket struct {
		Count        int     `json:"count"`
		TotalSeconds float64 `json:"total_seconds"`
		MaxSeconds   float64 `json:"max_seconds"`
	}
	sum := func(items []*observability.Span) bucket {
		var out bucket
		for _, span := range items {
			dur := traceDurationSeconds(span)
			out.Count++
			out.TotalSeconds += dur
			if dur > out.MaxSeconds {
				out.MaxSeconds = dur
			}
		}
		return out
	}
	var llm, tool, other []*observability.Span
	var errors []map[string]string
	for _, span := range spans {
		if span == nil {
			continue
		}
		switch {
		case strings.Contains(span.Operation, "llm_call"):
			llm = append(llm, span)
		case strings.Contains(span.Operation, "tool:"):
			tool = append(tool, span)
		default:
			other = append(other, span)
		}
		if span.Status == "error" {
			errors = append(errors, map[string]string{
				"operation": span.Operation,
				"error":     strings.TrimSpace(span.Tags["error"]),
			})
		}
	}
	return map[string]interface{}{
		"llm":    sum(llm),
		"tool":   sum(tool),
		"other":  sum(other),
		"errors": errors,
	}
}

func traceDurationSeconds(span *observability.Span) float64 {
	if span == nil || span.StartTime.IsZero() || span.EndTime.IsZero() {
		return 0
	}
	d := span.EndTime.Sub(span.StartTime).Seconds()
	if d < 0 {
		return 0
	}
	return d
}

type traceTreeNode struct {
	Span     *observability.Span `json:"span"`
	Children []*traceTreeNode    `json:"children,omitempty"`
}

type traceScopeSummary struct {
	Scope      string                `json:"scope,omitempty"`
	Workstream string                `json:"workstream,omitempty"`
	Decision   string                `json:"decision,omitempty"`
	Reason     string                `json:"reason,omitempty"`
	Score      int                   `json:"score,omitempty"`
	Threshold  int                   `json:"threshold,omitempty"`
	Candidates []traceScopeCandidate `json:"candidates,omitempty"`
}

type traceScopeCandidate struct {
	Scope      string `json:"scope"`
	Workstream string `json:"workstream,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Score      int    `json:"score"`
}

func traceRunLabel(spans []*observability.Span) string {
	for _, span := range spans {
		if span == nil || len(span.Tags) == 0 {
			continue
		}
		if run := strings.TrimSpace(span.Tags["sandbox_run"]); run != "" {
			return run
		}
	}
	return ""
}

func extractScopeSummary(spans []*observability.Span) traceScopeSummary {
	for _, span := range spans {
		if span == nil || len(span.Tags) == 0 {
			continue
		}
		scope := strings.TrimSpace(span.Tags["scope"])
		workstream := strings.TrimSpace(span.Tags["workstream"])
		decision := strings.TrimSpace(span.Tags["scope_decision"])
		reason := strings.TrimSpace(span.Tags["scope_reason"])
		if scope == "" && workstream == "" && decision == "" && reason == "" {
			continue
		}
		return traceScopeSummary{
			Scope:      scope,
			Workstream: workstream,
			Decision:   decision,
			Reason:     reason,
			Score:      parseTagInt(span.Tags["scope_score"]),
			Threshold:  parseTagInt(span.Tags["scope_threshold"]),
			Candidates: parseScopeCandidates(span.Tags["scope_candidates_json"]),
		}
	}
	return traceScopeSummary{}
}

func parseTagInt(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

func parseScopeCandidates(v string) []traceScopeCandidate {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []traceScopeCandidate
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil
	}
	return out
}

var debugDashboardTemplate = template.Must(template.New("debug_dashboard").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Nexus Debug Dashboard</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; margin: 24px; color: #111827; }
    h1, h2 { margin-bottom: 8px; }
    .muted { color: #6b7280; }
    .card { border: 1px solid #e5e7eb; border-radius: 10px; padding: 16px; margin: 12px 0; }
    .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
    .pill { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 12px; }
    .pill.ok { background: #dcfce7; color: #166534; }
    .pill.error { background: #fee2e2; color: #991b1b; }
    .pill.score-high { background: #dcfce7; color: #166534; }
    .pill.score-medium { background: #fef3c7; color: #92400e; }
    .pill.score-low { background: #ffedd5; color: #9a3412; }
    .error-row { background: #fef2f2; }
    code { background: #f3f4f6; padding: 2px 6px; border-radius: 6px; }
    pre { background: #0f172a; color: #e2e8f0; padding: 12px; border-radius: 10px; overflow-x: auto; }
    a { color: #2563eb; text-decoration: none; }
    table { width: 100%; border-collapse: collapse; }
    th, td { border-bottom: 1px solid #e5e7eb; text-align: left; padding: 8px; }
    details { margin: 8px 0; }
    .tree { font-family: ui-monospace, SFMono-Regular, monospace; white-space: pre-wrap; }
    .compact-table td, .compact-table th { padding: 6px; font-size: 13px; }
  </style>
</head>
<body>
  <h1>Nexus Debug Dashboard</h1>
  <p class="muted">Run filter: <code>{{if .Run}}{{.Run}}{{else}}all{{end}}</code></p>

  <div class="card">
    <h2>Quick Links</h2>
    <p><a href="{{.HealthURL}}">Health</a></p>
    <p><a href="{{.ScopesURL}}">Scopes JSON</a></p>
    <p><a href="{{.MetricsURL}}">Metrics JSON</a></p>
    <p><a href="{{.TracesURL}}">Trace List JSON</a></p>
  </div>

  <div class="card">
    <h2>Trace Summary</h2>
    <p class="muted">Matching traces: <code id="trace-count">{{.TraceCount}}</code></p>
    <div id="traces-loading">Loading trace list...</div>
    <table id="traces-table" hidden>
      <thead>
        <tr>
          <th>Operation</th>
          <th>Scope Decision</th>
          <th>Run</th>
          <th>Status</th>
          <th>Spans</th>
          <th>Request</th>
          <th>Trace</th>
          <th>Inspect</th>
        </tr>
      </thead>
      <tbody id="traces-body"></tbody>
    </table>
  </div>

  <div class="card">
    <h2>Scopes</h2>
    <div id="scopes-loading">Loading scopes...</div>
    <table id="scopes-table" hidden>
      <thead>
        <tr>
          <th>Scope</th>
          <th>Kind</th>
          <th>Lifecycle</th>
          <th>Workstream</th>
          <th>User</th>
          <th>Channel</th>
          <th>Updated</th>
          <th>Manager</th>
          <th>Directory</th>
        </tr>
      </thead>
      <tbody id="scopes-body"></tbody>
    </table>
  </div>

  <div class="grid">
    <div class="card">
      <h2>Metrics Snapshot</h2>
      <pre id="metrics">Loading metrics...</pre>
    </div>
    <div class="card">
      <h2>Derived Durations</h2>
      <div id="derived-summary">Select a trace to inspect timings.</div>
    </div>
  </div>

  <div class="card">
    <h2>Trace Detail</h2>
    <div id="trace-detail">Select a trace from the table to view its tree, grouped timings, and errors.</div>
  </div>

  <script>
    async function loadJSON(url) {
      const resp = await fetch(url);
      if (!resp.ok) {
        throw new Error(resp.status + " " + resp.statusText);
      }
      return await resp.json();
    }

    function esc(text) {
      return String(text ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
    }

    function durationSeconds(span) {
      if (!span || !span.start_time || !span.end_time) return 0;
      return Math.max(0, (Date.parse(span.end_time) - Date.parse(span.start_time)) / 1000);
    }

    function buildTree(spans) {
      const byId = new Map();
      const roots = [];
      for (const span of spans) {
        byId.set(span.span_id, { span, children: [] });
      }
      for (const span of spans) {
        const node = byId.get(span.span_id);
        const parent = byId.get(span.parent_id);
        if (parent) parent.children.push(node);
        else roots.push(node);
      }
      const sortNode = (node) => {
        node.children.sort((a, b) => Date.parse(a.span.start_time || 0) - Date.parse(b.span.start_time || 0));
        node.children.forEach(sortNode);
      };
      roots.sort((a, b) => Date.parse(a.span.start_time || 0) - Date.parse(b.span.start_time || 0));
      roots.forEach(sortNode);
      return roots;
    }

    function renderTree(nodes, depth = 0) {
      let out = "";
      for (const node of nodes) {
        const span = node.span || {};
        const dur = durationSeconds(span).toFixed(3) + "s";
        const indent = "  ".repeat(depth);
        const marker = span.status === "error" ? "[ERROR]" : "[OK]";
        out += indent + marker + " " + (span.operation || "") + " (" + dur + ")\n";
        if (span.tags && span.tags.error) {
          out += indent + "  error: " + span.tags.error + "\n";
        }
        out += renderTree(node.children || [], depth + 1);
      }
      return out;
    }

    function summarizeDurations(spans) {
      const groups = { llm: [], tool: [], other: [] };
      for (const span of spans) {
        const item = {
          operation: span.operation || "",
          seconds: durationSeconds(span),
          status: span.status || "ok"
        };
        if ((span.operation || "").includes("llm_call")) groups.llm.push(item);
        else if ((span.operation || "").includes("tool:")) groups.tool.push(item);
        else groups.other.push(item);
      }
      const summarize = (items) => ({
        count: items.length,
        total_seconds: items.reduce((acc, it) => acc + it.seconds, 0),
        max_seconds: items.reduce((acc, it) => Math.max(acc, it.seconds), 0)
      });
      return {
        llm: summarize(groups.llm),
        tool: summarize(groups.tool),
        other: summarize(groups.other),
        errors: spans.filter(span => span.status === "error").map(span => ({
          operation: span.operation || "",
          error: span.tags && span.tags.error ? span.tags.error : ""
        }))
      };
    }

    function scoreClass(score, threshold) {
      const s = Number(score || 0);
      const t = Number(threshold || 0);
      if (s <= 0 || t <= 0) return "";
      if (s >= t + 2) return "score-high";
      if (s >= t) return "score-medium";
      return "score-low";
    }

    function renderCandidatesTable(candidates) {
      const items = Array.isArray(candidates) ? candidates : [];
      if (items.length === 0) return "";
      const rows = items.map(item => {
        return "<tr>" +
          "<td><code>" + esc(item.scope || "") + "</code></td>" +
          "<td>" + esc(item.workstream || "") + "</td>" +
          "<td>" + esc(item.summary || "") + "</td>" +
          "<td>" + String(item.score || 0) + "</td>" +
          "</tr>";
      }).join("");
      return "<details open><summary>Scope Candidates</summary>" +
        "<table class='compact-table'><thead><tr><th>Scope</th><th>Workstream</th><th>Summary</th><th>Score</th></tr></thead><tbody>" +
        rows +
        "</tbody></table></details>";
    }

    async function inspectTrace(traceId) {
      const traceURL = "/api/debug/traces/" + encodeURIComponent(traceId) + ({{printf "%q" .Run}} ? ("?run=" + encodeURIComponent({{printf "%q" .Run}})) : "");
      const payload = await loadJSON(traceURL);
      const spans = Array.isArray(payload.spans) ? payload.spans : [];
      const tree = buildTree(spans);
      const summary = summarizeDurations(spans);
      const scopeSummary = payload.scope_summary || {};
      document.getElementById("derived-summary").textContent = JSON.stringify(summary, null, 2);

      let errorHTML = "";
      if (summary.errors.length > 0) {
        errorHTML = "<h3>Errors</h3><ul>" + summary.errors.map(item =>
          "<li><strong>" + esc(item.operation) + "</strong>: " + esc(item.error || "unknown error") + "</li>"
        ).join("") + "</ul>";
      }
      let scopeHTML = "";
      if (scopeSummary.scope || scopeSummary.workstream || scopeSummary.decision || scopeSummary.reason) {
        const qualityBits = [];
        if (scopeSummary.score || scopeSummary.threshold) {
          qualityBits.push("score=" + String(scopeSummary.score || 0));
          qualityBits.push("threshold=" + String(scopeSummary.threshold || 0));
        }
        if (scopeSummary.candidates && scopeSummary.candidates.length) {
          qualityBits.push("candidates=" + String(scopeSummary.candidates.length));
        }
        const qualityLine = qualityBits.length > 0
          ? "<p><strong>Quality:</strong> " + esc(qualityBits.join(", ")) + "</p>"
          : "";
        scopeHTML =
          "<details open><summary>Scope Decision</summary>" +
          "<p><strong>Scope:</strong> <code>" + esc(scopeSummary.scope || "") + "</code></p>" +
          "<p><strong>Workstream:</strong> " + esc(scopeSummary.workstream || "") + "</p>" +
          "<p><strong>Decision:</strong> " + esc(scopeSummary.decision || "") + "</p>" +
          "<p><strong>Reason:</strong> " + esc(scopeSummary.reason || "") + "</p>" +
          qualityLine +
          "<details><summary>Scope Decision JSON</summary><pre>" +
          esc(JSON.stringify(scopeSummary, null, 2)) +
          "</pre></details>" +
          renderCandidatesTable(scopeSummary.candidates) +
          "</details>";
      }
      document.getElementById("trace-detail").innerHTML =
        "<p><strong>Trace ID:</strong> <code>" + esc(traceId) + "</code></p>" +
        scopeHTML +
        "<details open><summary>Trace Tree</summary><pre class='tree'>" + esc(renderTree(tree)) + "</pre></details>" +
        "<details open><summary>Grouped Durations</summary><pre>" + esc(JSON.stringify(summary, null, 2)) + "</pre></details>" +
        "<details><summary>Raw Spans</summary><pre>" + esc(JSON.stringify(spans, null, 2)) + "</pre></details>" +
        errorHTML;
    }

    async function main() {
      try {
        const [metrics, traces, scopes] = await Promise.all([
          loadJSON({{printf "%q" .MetricsURL}}),
          loadJSON({{printf "%q" .TracesURL}}),
          loadJSON({{printf "%q" .ScopesURL}})
        ]);
        document.getElementById("metrics").textContent = JSON.stringify(metrics, null, 2);
        const items = Array.isArray(traces.traces) ? traces.traces : [];
        document.getElementById("trace-count").textContent = String(items.length);
        const body = document.getElementById("traces-body");
        const table = document.getElementById("traces-table");
        const loading = document.getElementById("traces-loading");
        loading.hidden = true;
        table.hidden = false;
        for (const item of items) {
          const row = document.createElement("tr");
          if (item.status === "error") row.className = "error-row";
          const badge = item.status === "error" ? "<span class='pill error'>error</span>" : "<span class='pill ok'>ok</span>";
          const scopeBits = [];
          if (item.scope) scopeBits.push(item.scope);
          if (item.scope_decision) {
            let decision = item.scope_decision;
            let decisionBadge = "";
            if (item.scope_score || item.scope_threshold) {
              const cls = scoreClass(item.scope_score, item.scope_threshold);
              decisionBadge = " <span class='pill " + cls + "'>" + String(item.scope_score || 0) + "/" + String(item.scope_threshold || 0) + "</span>";
            }
            scopeBits.push(decision + decisionBadge);
          }
          if (item.workstream) scopeBits.push(item.workstream);
          const scopeSummary = scopeBits.length > 0 ? scopeBits.join(" | ") : "<span class='muted'>n/a</span>";
          row.innerHTML =
            "<td>" + (item.operation || "") + "</td>" +
            "<td>" + scopeSummary + "</td>" +
            "<td>" + (item.run_label || "") + "</td>" +
            "<td>" + badge + "</td>" +
            "<td>" + String(item.span_count || 0) + "</td>" +
            "<td><code>" + (item.request_id || "") + "</code></td>" +
            "<td><a href='/api/debug/traces/" + encodeURIComponent(item.trace_id) + ({{printf "%q" .Run}} ? ("?run=" + encodeURIComponent({{printf "%q" .Run}})) : "") + "'>" + (item.trace_id || "") + "</a></td>" +
            "<td><button data-trace-id='" + (item.trace_id || "") + "'>Open</button></td>";
          body.appendChild(row);
        }
        body.querySelectorAll("button[data-trace-id]").forEach(btn => {
          btn.addEventListener("click", () => inspectTrace(btn.getAttribute("data-trace-id")));
        });
        const scopeItems = Array.isArray(scopes.scopes) ? scopes.scopes : [];
        const scopesBody = document.getElementById("scopes-body");
        const scopesTable = document.getElementById("scopes-table");
        const scopesLoading = document.getElementById("scopes-loading");
        scopesLoading.hidden = true;
        scopesTable.hidden = false;
        for (const item of scopeItems) {
          const row = document.createElement("tr");
          const status = item.manager_running ? "<span class='pill ok'>running</span>" : "<span class='pill'>idle</span>";
          const lifecycle = item.lifecycle ? "<span class='pill'>" + esc(item.lifecycle) + "</span>" : "<span class='muted'>n/a</span>";
          row.innerHTML =
            "<td><code>" + esc(item.scope || "") + "</code><br><span class='muted'>" + esc(item.summary || "") + "</span></td>" +
            "<td>" + esc(item.scope_kind || "") + "<br><span class='muted'>bucket: " + esc(item.storage_bucket || "") + "</span></td>" +
            "<td>" + lifecycle + "</td>" +
            "<td>" + esc(item.workstream || "") + "<br><span class='muted'>" + esc((item.keywords || []).join(', ')) + "</span></td>" +
            "<td>" + esc(item.user || "") + "</td>" +
            "<td>" + esc(item.channel || "") + "</td>" +
            "<td>" + esc(item.updated_at || "") + "</td>" +
            "<td>" + status + "<br><span class='muted'>" + esc(item.manager_last_used_at || "") + "</span></td>" +
            "<td><code>" + esc(item.team_dir || "") + "</code></td>";
          scopesBody.appendChild(row);
        }
        if (items.length > 0 && items[0].trace_id) {
          inspectTrace(items[0].trace_id).catch(err => {
            document.getElementById("trace-detail").textContent = "Failed to load trace detail: " + String(err);
          });
        }
      } catch (err) {
        document.getElementById("metrics").textContent = String(err);
        document.getElementById("traces-loading").textContent = "Failed to load trace list: " + String(err);
      }
    }
    main();
  </script>
</body>
</html>`))
