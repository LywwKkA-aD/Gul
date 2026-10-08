"""Native prompt protocol proof inside a caller-created private D-Bus session."""
import os
import signal
import select
import subprocess
import sys


def run(helper, fixture, mode, action, expected):
    server = subprocess.Popen([fixture, mode], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    try:
        if server.stdout.readline() != b"GUL_TEST_SERVICE_READY\n":
            raise RuntimeError("GUL_PASSWORD_STORE_PROOF_FAILED")
        if mode == "pending":
            child = subprocess.Popen([helper, action, "GulProof"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            ready, _, _ = select.select([server.stdout], [], [], 7)
            if not ready or server.stdout.readline() != b"GUL_TEST_PROMPT_READY\n":
                child.kill()
                child.wait(timeout=3)
                raise RuntimeError("GUL_PASSWORD_STORE_PROMPT_NOT_READY")
            child.send_signal(signal.SIGTERM)
            output, _ = child.communicate(timeout=7)
            if child.returncode != 0:
                raise RuntimeError("GUL_PASSWORD_STORE_CANCEL_FAILED")
        else:
            result = subprocess.run([helper, action, "GulProof"], capture_output=True, timeout=7)
            if result.returncode != 0:
                raise RuntimeError("GUL_PASSWORD_STORE_PROOF_FAILED")
            output = result.stdout
        if output != f"GUL_PASSWORD_STORE_{expected}\n".encode():
            raise RuntimeError("GUL_PASSWORD_STORE_PROOF_FAILED")
    finally:
        server.terminate()
        server.wait(timeout=3)


if __name__ == "__main__":
    if not os.environ.get("DBUS_SESSION_BUS_ADDRESS"):
        raise SystemExit("GUL_PASSWORD_STORE_PROOF_REQUIRES_PRIVATE_BUS")
    helper, fixture = sys.argv[1:]
    for case in [
        ("locked", "--status", "LOCKED"), ("ready", "--status", "READY"),
        ("missing", "--status", "MISSING"), ("locked", "--unlock", "READY"),
        ("cancel", "--unlock", "CANCELLED"), ("refuse", "--unlock", "UNAVAILABLE"),
        ("pending", "--unlock", "CANCELLED"),
    ]:
        run(helper, fixture, *case)
    print("GUL_PASSWORD_STORE_NATIVE_PROOF_PASSED")
