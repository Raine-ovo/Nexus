import argparse
import json
import subprocess
import sys
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
EXP_ROOT = Path(__file__).resolve().parent


def wait_http_ready(url: str, timeout: float = 60.0, headers=None) -> None:
    import urllib.request

    headers = headers or {}
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            req = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(req, timeout=5) as resp:
                if resp.status == 200:
                    return
        except Exception:
            time.sleep(1.0)
    raise RuntimeError(f"service not ready: {url}")


def terminate(proc: subprocess.Popen):
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def run_phase(args, phase, prompt_name, *, workstream="", run_ws_probe=False):
    cmd = [
        sys.executable,
        str(EXP_ROOT / "run_experiment.py"),
        "--phase",
        phase,
        "--base-url",
        args.base_url,
        "--ws-url",
        args.ws_url,
        "--api-key",
        args.api_key,
        "--sandbox",
        str(args.sandbox),
        "--prompt",
        str(EXP_ROOT / prompt_name),
        "--run-name",
        args.run_name,
        "--channel",
        args.run_name,
        "--session-user",
        "tester",
    ]
    if workstream:
        cmd += ["--workstream", workstream]
    if run_ws_probe:
        cmd.append("--run-ws-probe")
    result = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True)
    phase_dir = Path(args.sandbox) / "experiment" / phase
    (phase_dir / "runner_stdout.txt").write_text(result.stdout)
    (phase_dir / "runner_stderr.txt").write_text(result.stderr)
    if result.returncode != 0:
        raise RuntimeError(f"{phase} failed: {result.stderr or result.stdout}")
    return json.loads(result.stdout)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default=str(EXP_ROOT / "config.yaml"))
    parser.add_argument("--sandbox", default=str(ROOT / ".runs" / "scope-continuity-validation"))
    parser.add_argument("--base-url", default="http://127.0.0.1:18224")
    parser.add_argument("--ws-url", default="ws://127.0.0.1:18225/api/ws")
    parser.add_argument("--api-key", default="nexus-local-dev-key")
    parser.add_argument("--run-name", default="scope-continuity-validation")
    parser.add_argument("--workstream", default="scope continuity validation workstream")
    parser.add_argument("--keep-nexus-alive", action="store_true")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    sandbox = Path(args.sandbox)
    art_dir = sandbox / "experiment"
    log_dir = art_dir / "orchestrator_logs"
    art_dir.mkdir(parents=True, exist_ok=True)
    log_dir.mkdir(parents=True, exist_ok=True)

    mock_log = open(log_dir / "mcp_mock.log", "w")
    nexus_log = open(log_dir / "nexus.log", "w")
    mock = None
    nexus = None
    try:
        mock = subprocess.Popen(
            [sys.executable, str(ROOT / "experiments" / "all-features" / "mcp_mock_server.py")],
            cwd=ROOT,
            stdout=mock_log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        wait_http_ready("http://127.0.0.1:18110/mcp/sse", timeout=20.0)

        def start_nexus():
            return subprocess.Popen(
                ["go", "run", "./cmd/nexus", "-config", args.config],
                cwd=ROOT,
                stdout=nexus_log,
                stderr=subprocess.STDOUT,
                text=True,
            )

        nexus = start_nexus()
        wait_http_ready(
            args.base_url.rstrip("/") + "/api/health",
            timeout=60.0,
            headers={"X-API-Key": args.api_key},
        )

        phase1 = run_phase(args, "phase1_main_task", "prompt.txt", workstream=args.workstream)
        phase2 = run_phase(args, "phase2_new_session_continuation", "phase2_continuation_prompt.txt", run_ws_probe=True)

        terminate(nexus)
        nexus = start_nexus()
        wait_http_ready(
            args.base_url.rstrip("/") + "/api/health",
            timeout=60.0,
            headers={"X-API-Key": args.api_key},
        )

        phase3 = run_phase(args, "phase3_restart_resume", "phase3_restart_resume_prompt.txt")

        overall = {
            "sandbox": str(sandbox),
            "artifacts_dir": str(art_dir),
            "run_name": args.run_name,
            "workstream": args.workstream,
            "phase1": phase1,
            "phase2": phase2,
            "phase3": phase3,
            "phase2_reused_scope": bool(phase1.get("trace_scope") and phase1.get("trace_scope") == phase2.get("trace_scope")),
            "phase3_reused_scope_after_restart": bool(
                phase1.get("trace_scope") and phase1.get("trace_scope") == phase3.get("trace_scope")
            ),
            "phase2_continuation_like": "continuation" in (phase2.get("trace_decision") or ""),
            "phase3_continuation_like": "continuation" in (phase3.get("trace_decision") or ""),
            "finished_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        }
        overall["continuity_passed"] = bool(
            overall["phase2_reused_scope"]
            and overall["phase3_reused_scope_after_restart"]
            and overall["phase2_continuation_like"]
            and overall["phase3_continuation_like"]
        )
        (art_dir / "summary.json").write_text(json.dumps(overall, ensure_ascii=False, indent=2))
        print(json.dumps(overall, ensure_ascii=False, indent=2))

        if args.keep_nexus_alive:
            while True:
                time.sleep(5)
        return 0
    finally:
        if not args.keep_nexus_alive:
            terminate(nexus)
        terminate(mock)
        mock_log.close()
        nexus_log.close()


if __name__ == "__main__":
    raise SystemExit(main())
