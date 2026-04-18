import argparse
import json
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
    path.parent.mkdir(parents=True, exist_ok=True)
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


def latest_trace_id(base_url: str, api_key: str, run_name: str):
    traces = request_json(
        "GET",
        f"{base_url}/api/debug/traces?run={run_name}",
        headers={"X-API-Key": api_key},
        timeout=60,
    )
    items = traces.get("traces") or []
    if not items:
        return "", traces
    return items[0].get("trace_id", ""), traces


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--phase", required=True)
    parser.add_argument("--base-url", default="http://127.0.0.1:18224")
    parser.add_argument("--ws-url", default="ws://127.0.0.1:18225/api/ws")
    parser.add_argument("--api-key", default="nexus-local-dev-key")
    parser.add_argument("--sandbox", default=str(ROOT / ".runs" / "scope-continuity-validation"))
    parser.add_argument("--prompt", required=True)
    parser.add_argument("--run-name", default="scope-continuity-validation")
    parser.add_argument("--session-user", default="tester")
    parser.add_argument("--channel", default="scope-continuity-validation")
    parser.add_argument("--workstream", default="")
    parser.add_argument("--run-ws-probe", action="store_true")
    return parser.parse_args()


def main():
    args = parse_args()
    base_url = args.base_url.rstrip("/")
    sandbox = Path(args.sandbox)
    phase_dir = sandbox / "experiment" / args.phase
    phase_dir.mkdir(parents=True, exist_ok=True)
    prompt = Path(args.prompt).read_text()
    wait_ready(base_url, args.api_key)

    summary = {
        "phase": args.phase,
        "sandbox": str(sandbox),
        "run_name": args.run_name,
        "started_at": datetime.now(timezone.utc).isoformat(),
        "warnings": [],
    }

    session_payload = {
        "channel": args.channel,
        "user": args.session_user,
    }
    if args.workstream.strip():
        session_payload["workstream"] = args.workstream.strip()
    session = request_json(
        "POST",
        base_url + "/api/sessions",
        session_payload,
        headers={"X-API-Key": args.api_key},
        timeout=60,
    )
    write_json(phase_dir / "session.json", session)
    summary["session_id"] = session.get("session_id", "")
    summary["session_scope"] = session.get("scope", "")
    summary["session_workstream"] = session.get("workstream", "")

    job = request_json(
        "POST",
        base_url + "/api/chat/jobs",
        {"session_id": session["session_id"], "input": prompt, "lane": "main"},
        headers={"X-API-Key": args.api_key},
        timeout=60,
    )
    write_json(phase_dir / "job_created.json", job)
    summary["job_id"] = job.get("job_id", "")

    result = poll_job(base_url, args.api_key, job["job_id"])
    write_json(phase_dir / "job_result.json", result)
    summary["job_status"] = result.get("status")
    summary["job_error"] = result.get("error", "")
    summary["final_output_excerpt"] = (result.get("output") or "")[:3000]

    endpoints = {
        "health.json": base_url + "/api/health",
        "metrics.json": base_url + f"/api/debug/metrics?run={args.run_name}",
        "scopes.json": base_url + "/api/debug/scopes",
        "traces.json": base_url + f"/api/debug/traces?run={args.run_name}",
        "dashboard.html": base_url + f"/debug/dashboard?run={args.run_name}",
    }
    for name, url in endpoints.items():
        out = safe_fetch(url, args.api_key)
        if name.endswith(".html"):
            (phase_dir / name).write_text(out["body"])
        else:
            write_json(phase_dir / name, out)

    trace_id, traces_payload = latest_trace_id(base_url, args.api_key, args.run_name)
    if trace_id:
        detail = request_json(
            "GET",
            f"{base_url}/api/debug/traces/{trace_id}?run={args.run_name}",
            headers={"X-API-Key": args.api_key},
            timeout=60,
        )
        write_json(phase_dir / "trace_detail.json", detail)
        scope_summary = detail.get("scope_summary") or {}
        summary["trace_id"] = trace_id
        summary["trace_scope"] = scope_summary.get("scope", "")
        summary["trace_workstream"] = scope_summary.get("workstream", "")
        summary["trace_decision"] = scope_summary.get("decision", "")
        summary["trace_reason"] = scope_summary.get("reason", "")
        summary["trace_score"] = scope_summary.get("score", 0)
        summary["trace_threshold"] = scope_summary.get("threshold", 0)
        summary["trace_candidates"] = len(scope_summary.get("candidates") or [])
    else:
        summary["warnings"].append("no_trace_found")
        write_json(phase_dir / "trace_detail.json", {"error": "no trace found", "traces": traces_payload})

    latest = sandbox / "latest-traces.json"
    if latest.exists():
        (phase_dir / "latest-traces.json").write_text(latest.read_text())
    else:
        summary["warnings"].append("latest_traces_missing")

    if args.run_ws_probe:
        ws_out = phase_dir / "ws_followup.json"
        probe = subprocess.run(
            [
                "go",
                "run",
                "./experiments/scope-continuity-validation/ws_probe.go",
                "--url",
                args.ws_url,
                "--session",
                session["session_id"],
                "--input",
                "请继续当前这条 scope continuity 实验工作线，仅用简短摘要说明你沿用的输出目录、scope 相关证据以及本轮新增文件。",
                "--out",
                str(ws_out),
                "--api-key",
                args.api_key,
            ],
            cwd=ROOT,
            text=True,
            capture_output=True,
        )
        write_json(
            phase_dir / "ws_probe_result.json",
            {"returncode": probe.returncode, "stdout": probe.stdout, "stderr": probe.stderr},
        )
        summary["ws_probe_returncode"] = probe.returncode
        if probe.returncode != 0:
            summary["warnings"].append("ws_probe_failed")

    summary["finished_at"] = datetime.now(timezone.utc).isoformat()
    write_json(phase_dir / "phase_summary.json", summary)
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
