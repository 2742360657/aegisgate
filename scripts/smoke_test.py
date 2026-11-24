#!/usr/bin/env python3
"""Real-process smoke test. Uses ephemeral loopback ports and cleans up its children."""
import concurrent.futures
import http.server
import json
from pathlib import Path
import signal
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    children = []
    handles = []
    with tempfile.TemporaryDirectory(prefix="aegisgate-smoke-") as temporary:
        scratch = Path(temporary)

        def start(name, command):
            path = scratch / (name + ".log")
            handle = path.open("w")
            handles.append(handle)
            process = subprocess.Popen(command, cwd=ROOT, stdout=handle, stderr=subprocess.STDOUT)
            children.append(process)
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                for line in path.read_text().splitlines():
                    try:
                        address = json.loads(line).get("address")
                    except json.JSONDecodeError:
                        continue
                    if address:
                        return process, "http://" + address
                if process.poll() is not None:
                    raise RuntimeError(name + " exited: " + path.read_text())
                time.sleep(0.02)
            raise TimeoutError(name + " did not start: " + path.read_text())

        def call(url, body=None, headers=None):
            request = urllib.request.Request(url, data=body, headers=headers or {})
            try:
                response = urllib.request.urlopen(request, timeout=5)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                return response.status, response.headers, response.read()

        reached, release = threading.Event(), threading.Event()

        class ControlledSlowUpstream(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                reached.set()
                if not release.wait(5):
                    self.send_error(500)
                    return
                self.send_response(200)
                self.send_header("Content-Length", "7")
                self.end_headers()
                self.wfile.write(b"drained")

            def log_message(self, *args):
                pass

        slow = http.server.ThreadingHTTPServer(("127.0.0.1", 0), ControlledSlowUpstream)
        thread = threading.Thread(target=slow.serve_forever, daemon=True)
        thread.start()
        try:
            _, first = start("node-a", [str(ROOT / "bin/mock-upstream"), "-listen", "127.0.0.1:0", "-name", "node-a"])
            _, second = start("node-b", [str(ROOT / "bin/mock-upstream"), "-listen", "127.0.0.1:0", "-name", "node-b"])
            config = scratch / "gateway.yaml"
            config.write_text(
                "server:\n  listen: 127.0.0.1:0\n  max_body_bytes: 8\n"
                "  request_timeout: 3s\n  shutdown_timeout: 4s\n"
                "upstreams:\n  demo: [" + first + ", " + second + "]\n"
                "  slow: [http://127.0.0.1:" + str(slow.server_port) + "]\n"
                "routes:\n  - id: demo\n    path_prefix: /\n    upstream: demo\n"
                "  - id: slow\n    path_prefix: /slow\n    upstream: slow\n"
            )
            gateway, front = start("gateway", [str(ROOT / "bin/gateway"), "-config", str(config)])
            assert call(front + "/healthz")[0] == 200
            replies = [json.loads(call(front + "/echo")[2])["upstream"] for _ in range(4)]
            assert replies == ["node-a", "node-b", "node-a", "node-b"], replies
            status, headers, raw = call(front + "/echo?q=hi", b"hello", {
                "X-Forwarded-For": "8.8.8.8", "X-Request-ID": "spoofed",
            })
            echo = json.loads(raw)
            assert status == 200 and echo["body"] == "hello" and echo["query"] == "q=hi"
            assert echo["forwarded_for"] == "127.0.0.1"
            assert echo["request_id"] == headers["X-Request-ID"] != "spoofed"
            assert call(front + "/echo", b"123456789")[0] == 413
            print("PASS: real binary startup, health, dual-node round-robin, forwarding, trusted headers, body limit")

            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
                in_flight = executor.submit(call, front + "/slow")
                assert reached.wait(3), "slow request never reached upstream"
                gateway.send_signal(signal.SIGINT)
                deadline = time.monotonic() + 2
                while time.monotonic() < deadline:
                    if "shutdown started" in (scratch / "gateway.log").read_text():
                        break
                    time.sleep(0.01)
                else:
                    raise AssertionError("gateway did not enter shutdown")
                assert gateway.poll() is None, "gateway exited while request was still in flight"
                release.set()
                status, _, raw = in_flight.result(timeout=4)
                assert status == 200 and raw == b"drained"
                assert gateway.wait(timeout=5) == 0
            print("PASS: SIGINT stops acceptance and drains an already-forwarding slow request")
        except Exception:
            for path in scratch.glob("*.log"):
                print("---", path.name, "---")
                print(path.read_text())
            raise
        finally:
            release.set()
            slow.shutdown()
            slow.server_close()
            thread.join(timeout=2)
            for process in reversed(children):
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=3)
            for handle in handles:
                handle.close()


if __name__ == "__main__":
    main()
