import json
import subprocess
import sys
import time
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
EXP = Path(__file__).resolve().parent
SANDBOX = ROOT / ".runs" / "scope-continuity-smoke"
ART = SANDBOX / "experiment"
LOG = ART / "manual_logs"
API_KEY = "nexus-local-dev-key"
BASE = "http://127.0.0.1:18234"
WS = "ws://127.0.0.1:18235/api/ws"
WORKSTREAM = "smoke scope continuity validation"
CONFIG = str(EXP / "config.smoke.yaml")


def wait_ready(url, timeout=60, headers=None):
    headers = headers or {}
    end = time.time() + timeout
    while time.time() < end:
        try:
            req = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(req, timeout=5) as resp:
                if resp.status == 200:
                    return True
        except Exception:
            time.sleep(1)
    return False


def stop(proc):
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def start_nexus():
    LOG.mkdir(parents=True, exist_ok=True)
    return subprocess.Popen(
        ["go", "run", "./cmd/nexus", "-config", CONFIG],
        cwd=ROOT,
        stdout=open(LOG / "nexus.log", "a"),
        stderr=subprocess.STDOUT,
        text=True,
    )


def run_phase(name, prompt_file, workstream=""):
    cmd = [
        sys.executable,
        str(EXP / "run_experiment.py"),
        "--phase",
        name,
        "--base-url",
        BASE,
        "--ws-url",
        WS,
        "--api-key",
        API_KEY,
        "--sandbox",
        str(SANDBOX),
        "--prompt",
        str(EXP / prompt_file),
        "--run-name",
        "scope-continuity-smoke",
        "--channel",
        "scope-continuity-smoke",
        "--session-user",
        "tester",
    ]
    if workstream:
        cmd += ["--workstream", workstream]
    res = subprocess.run(cmd, cwd=ROOT, text=True, capture_output=True)
    phase_dir = ART / name
    phase_dir.mkdir(parents=True, exist_ok=True)
    (phase_dir / "runner_stdout.txt").write_text(res.stdout)
    (phase_dir / "runner_stderr.txt").write_text(res.stderr)
    if res.returncode != 0:
        raise RuntimeError(f"phase {name} failed: {res.stderr or res.stdout}")
    return json.loads(res.stdout)


def main():
    nexus = start_nexus()
    try:
        if not wait_ready(BASE + "/api/health", timeout=60, headers={"X-API-Key": API_KEY}):
            raise SystemExit("nexus not ready")
        p1 = run_phase("phase1_smoke", "phase1_smoke_prompt.txt", workstream=WORKSTREAM)
        p2 = run_phase("phase2_smoke", "phase2_smoke_prompt.txt")
        stop(nexus)
        nexus = start_nexus()
        if not wait_ready(BASE + "/api/health", timeout=60, headers={"X-API-Key": API_KEY}):
            raise SystemExit("nexus not ready after restart")
        p3 = run_phase("phase3_smoke", "phase3_smoke_prompt.txt")
        summary = {
            "phase1": p1,
            "phase2": p2,
            "phase3": p3,
            "phase2_reused_scope": p1.get("trace_scope") == p2.get("trace_scope") and bool(p2.get("trace_scope")),
            "phase3_reused_scope": p1.get("trace_scope") == p3.get("trace_scope") and bool(p3.get("trace_scope")),
            "phase2_decision": p2.get("trace_decision"),
            "phase3_decision": p3.get("trace_decision"),
        }
        summary["continuity_passed"] = bool(summary["phase2_reused_scope"] and summary["phase3_reused_scope"])
        (ART / "summary.smoke.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2))
        print(json.dumps(summary, ensure_ascii=False, indent=2))
    finally:
        stop(nexus)


if __name__ == "__main__":
    main()
