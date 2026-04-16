import json
import urllib.request
from pathlib import Path

base = "http://127.0.0.1:18084"
prompt = Path(".outputs/governance_prompt.txt").read_text()

req = urllib.request.Request(
    base + "/api/sessions",
    data=json.dumps({"channel": "governance-run", "user": "tester"}).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urllib.request.urlopen(req, timeout=30) as r:
    session = json.loads(r.read().decode())

payload = {"session_id": session["session_id"], "input": prompt, "lane": "main"}
req = urllib.request.Request(
    base + "/api/chat",
    data=json.dumps(payload).encode(),
    headers={"Content-Type": "application/json"},
    method="POST",
)

try:
    with urllib.request.urlopen(req, timeout=1200) as r:
        body = r.read().decode()
        status = r.status
except urllib.error.HTTPError as e:
    body = e.read().decode()
    status = e.code

out = Path(".runs/governance/forced_response.json")
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(
    json.dumps(
        {"session": session, "status": status, "response_raw": body},
        ensure_ascii=False,
        indent=2,
    )
)
print(out.resolve())
