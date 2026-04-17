import json
import argparse
import subprocess
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


def request_json(method: str, url: str, body=None, headers=None, timeout=60):
    headers = headers or {}
    if body is not None:
        data = json.dumps(body).encode()
        headers = {"Content-Type": "application/json", **headers}
    else:
        data = None
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read().decode()
        if not raw:
            return {}
        return json.loads(raw)


def safe_fetch(url: str, api_key: str):
    req = urllib.request.Request(url, headers={"X-API-Key": api_key}, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return {"status": resp.status, "body": resp.read().decode()}
    except urllib.error.HTTPError as e:
        return {"status": e.code, "body": e.read().decode()}


def write_json(path: Path, data):
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2))


def wait_ready(base_url: str, api_key: str):
    for _ in range(90):
        try:
            out = safe_fetch(base_url + "/api/health", api_key)
            if out["status"] == 200:
                return
        except Exception:
            pass
        time.sleep(2)
    raise RuntimeError("nexus server did not become ready")


def poll_job(base_url: str, api_key: str, job_id: str):
    for _ in range(900):
        body = request_json(
            "GET",
            f"{base_url}/api/chat/jobs/{job_id}",
            headers={"X-API-Key": api_key},
            timeout=60,
        )
        if body.get("status") in {"succeeded", "failed"}:
            return body
        time.sleep(2)
    raise RuntimeError("job polling timed out")


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:18194")
    parser.add_argument("--ws-url", default="ws://127.0.0.1:18195/api/ws")
    parser.add_argument("--api-key", default="nexus-local-dev-key")
    parser.add_argument("--sandbox", default=str(ROOT / ".runs" / "all-features-exp"))
    parser.add_argument("--prompt", default=str(ROOT / "experiments" / "all-features" / "activation_prompt.txt"))
    parser.add_argument("--run-name", default="all-features-exp")
    return parser.parse_args()


def main():
    args = parse_args()
    sandbox = Path(args.sandbox)
    art_dir = sandbox / "experiment"
    prompt_path = Path(args.prompt)
    base_url = args.base_url.rstrip("/")
    ws_url = args.ws_url
    api_key = args.api_key
    run_name = args.run_name

    art_dir.mkdir(parents=True, exist_ok=True)
    wait_ready(base_url, api_key)
    summary = {
        "sandbox": str(sandbox),
        "artifacts_dir": str(art_dir),
        "run_name": run_name,
        "started_at": datetime.now(timezone.utc).isoformat(),
        "warnings": [],
    }

    prompt = prompt_path.read_text()
    session = request_json(
        "POST",
        base_url + "/api/sessions",
        {"channel": run_name, "user": "tester"},
        headers={"X-API-Key": api_key},
    )
    write_json(art_dir / "session.json", session)
    summary["session_id"] = session["session_id"]

    job = request_json(
        "POST",
        base_url + "/api/chat/jobs",
        {"session_id": session["session_id"], "input": prompt, "lane": "main"},
        headers={"X-API-Key": api_key},
        timeout=60,
    )
    write_json(art_dir / "job_created.json", job)
    summary["job_id"] = job["job_id"]

    result = poll_job(base_url, api_key, job["job_id"])
    write_json(art_dir / "job_result.json", result)
    summary["job_status"] = result.get("status")
    summary["job_error"] = result.get("error", "")
    summary["final_output_excerpt"] = (result.get("output") or "")[:2000]

    ws_proc = subprocess.run(
        [
            "go",
            "run",
            "./experiments/all-features/ws_probe.go",
            "--url",
            ws_url,
            "--session",
            session["session_id"],
            "--input",
            "请仅用简短摘要说明这次实验实际触发了哪些 agent、哪些文件被写出，以及是否出现 mcp_repo_probe、delegate_task、spawn_teammate、send_message、claim_task、reflection、memory 落盘。",
            "--out",
            str(art_dir / "ws_followup.json"),
            "--api-key",
            api_key,
        ],
        cwd=ROOT,
        text=True,
        capture_output=True,
    )
    write_json(
        art_dir / "ws_probe_result.json",
        {"returncode": ws_proc.returncode, "stdout": ws_proc.stdout, "stderr": ws_proc.stderr},
    )
    if ws_proc.returncode != 0:
        summary["warnings"].append("ws_probe_failed")

    debug_endpoints = {
        "health.json": base_url + "/api/health",
        "metrics.json": base_url + f"/api/debug/metrics?run={run_name}",
        "traces.json": base_url + f"/api/debug/traces?run={run_name}",
        "dashboard.html": base_url + f"/debug/dashboard?run={run_name}",
        "mcp_tools_list.json": base_url + "/mcp/rpc",
    }
    for name, url in debug_endpoints.items():
        if name == "mcp_tools_list.json":
            payload = {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "tools/list",
                "params": {},
            }
            req = urllib.request.Request(
                url,
                data=json.dumps(payload).encode(),
                headers={"Content-Type": "application/json", "X-API-Key": api_key},
                method="POST",
            )
            with urllib.request.urlopen(req, timeout=60) as resp:
                body = resp.read().decode()
            (art_dir / name).write_text(body)
        else:
            out = safe_fetch(url, api_key)
            (art_dir / name).write_text(
                out["body"] if name.endswith(".html") else json.dumps(out, ensure_ascii=False, indent=2)
            )

    latest = sandbox / "latest-traces.json"
    if latest.exists():
        (art_dir / "latest-traces.json").write_text(latest.read_text())
    else:
        summary["warnings"].append("latest_traces_missing")

    summary["finished_at"] = datetime.now(timezone.utc).isoformat()
    write_json(art_dir / "summary.json", summary)
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
