import json
import urllib.request
from pathlib import Path

base = "http://127.0.0.1:18082"
prompt = Path(".outputs/forced_team_prompt.txt").read_text()

req = urllib.request.Request(
    base + "/api/sessions",
    data=json.dumps({"channel": "forced-team", "user": "tester"}).encode(),
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
with urllib.request.urlopen(req, timeout=1200) as r:
    body = r.read().decode()

Path(".outputs/forced_team_response.json").write_text(
    json.dumps({"session": session, "response_raw": body}, ensure_ascii=False, indent=2)
)
print(Path(".outputs/forced_team_response.json").resolve())
