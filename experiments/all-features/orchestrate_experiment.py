import argparse
import subprocess
import sys
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


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
    if proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", default="configs/all-features.experiment.yaml")
    parser.add_argument("--sandbox", default=str(ROOT / ".runs" / "all-features-exp"))
    parser.add_argument("--base-url", default="http://127.0.0.1:18194")
    parser.add_argument("--ws-url", default="ws://127.0.0.1:18195/api/ws")
    parser.add_argument("--api-key", default="nexus-local-dev-key")
    parser.add_argument("--prompt", default=str(ROOT / "experiments" / "all-features" / "activation_prompt.txt"))
    parser.add_argument("--run-name", default="all-features-exp")
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

    mock = subprocess.Popen(
        [sys.executable, "experiments/all-features/mcp_mock_server.py"],
        cwd=ROOT,
        stdout=mock_log,
        stderr=subprocess.STDOUT,
        text=True,
    )
    try:
        wait_http_ready("http://127.0.0.1:18110/mcp/sse", timeout=20.0)

        nexus = subprocess.Popen(
            ["go", "run", "./cmd/nexus", "-config", args.config],
            cwd=ROOT,
            stdout=nexus_log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        try:
            wait_http_ready(
                args.base_url.rstrip("/") + "/api/health",
                timeout=60.0,
                headers={"X-API-Key": args.api_key},
            )

            run = subprocess.run(
                [
                    sys.executable,
                    "experiments/all-features/run_experiment.py",
                    "--base-url", args.base_url,
                    "--ws-url", args.ws_url,
                    "--api-key", args.api_key,
                    "--sandbox", str(sandbox),
                    "--prompt", args.prompt,
                    "--run-name", args.run_name,
                ],
                cwd=ROOT,
                text=True,
                capture_output=True,
            )
            (art_dir / "orchestrator_stdout.txt").write_text(run.stdout)
            (art_dir / "orchestrator_stderr.txt").write_text(run.stderr)
            if args.keep_nexus_alive:
                while True:
                    time.sleep(5)
            return run.returncode
        finally:
            if not args.keep_nexus_alive:
                terminate(nexus)
    finally:
        terminate(mock)
        mock_log.close()
        nexus_log.close()


if __name__ == "__main__":
    raise SystemExit(main())
