package gateway

import (
	"html/template"
	"net/http"
)

// handleDebugApprovals renders a minimal operator UI for reviewing pending
// human-in-the-loop approvals.
func (g *Gateway) handleDebugApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = approvalsUITemplate.Execute(w, nil)
}

var approvalsUITemplate = template.Must(template.New("approvals_ui").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Nexus Approvals</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, sans-serif; margin: 24px; color: #111827; }
  h1 { margin-bottom: 4px; }
  .muted { color: #6b7280; }
  table { width: 100%; border-collapse: collapse; margin-top: 16px; }
  th, td { border-bottom: 1px solid #e5e7eb; text-align: left; padding: 8px; vertical-align: top; }
  code { background: #f3f4f6; padding: 2px 6px; border-radius: 6px; word-break: break-all; }
  button { margin-right: 6px; padding: 4px 10px; cursor: pointer; border: 1px solid #d1d5db; border-radius: 6px; background: #fff; }
  button.approve { background: #dcfce7; border-color: #86efac; }
  button.deny { background: #fee2e2; border-color: #fca5a5; }
  .empty { color: #6b7280; padding: 16px; }
</style>
</head>
<body>
  <h1>Pending Approvals</h1>
  <p class="muted">Review human-in-the-loop tool approvals. Auto-refreshes every 5s.</p>
  <table id="approvals">
    <thead><tr><th>Tool</th><th>Arguments</th><th>Reason</th><th>Created</th><th>Actions</th></tr></thead>
    <tbody></tbody>
  </table>

  <script>
    function esc(s) { return String(s ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;"); }

    async function load() {
      const resp = await fetch("/api/approvals");
      const data = await resp.json();
      const tbody = document.querySelector("#approvals tbody");
      tbody.innerHTML = "";
      const items = Array.isArray(data.approvals) ? data.approvals : [];
      if (items.length === 0) {
        tbody.innerHTML = '<tr><td colspan="5" class="empty">No pending approvals.</td></tr>';
        return;
      }
      for (const a of items) {
        const tr = document.createElement("tr");
        const args = JSON.stringify(a.arguments ?? {});
        tr.innerHTML =
          "<td><code>" + esc(a.tool_name) + "</code></td>" +
          "<td><code>" + esc(args) + "</code></td>" +
          "<td>" + esc(a.reason) + "</td>" +
          "<td>" + esc(a.created_at) + "</td>" +
          '<td>' +
            '<button class="approve" data-id="' + esc(a.id) + '" data-persist="false">Approve once</button>' +
            '<button class="approve" data-id="' + esc(a.id) + '" data-persist="true">Approve always</button>' +
            '<button class="deny" data-id="' + esc(a.id) + '" data-deny="true">Deny</button>' +
          "</td>";
        tbody.appendChild(tr);
      }
      tbody.querySelectorAll("button").forEach(btn => btn.addEventListener("click", () => act(btn)));
    }

    async function act(btn) {
      const id = btn.dataset.id;
      const deny = btn.dataset.deny === "true";
      const url = "/api/approvals/" + encodeURIComponent(id) + (deny ? "/deny" : "/approve");
      const opts = { method: "POST", headers: { "Content-Type": "application/json" } };
      if (!deny) opts.body = JSON.stringify({ persist: btn.dataset.persist === "true" });
      await fetch(url, opts);
      load();
    }

    load();
    setInterval(load, 5000);
  </script>
</body>
</html>`))
