"""Real server regression: game in the private mix, Gul only in the audible output."""
import argparse
import json
import math
import os
import secrets
import select
import signal
import struct
import subprocess
import sys
import tempfile
import threading
import time


def pulse(*arguments):
    return subprocess.check_output(["pactl", *arguments], text=True).strip()


def pcm(path, frequency, seconds=30):
    with open(path, "wb") as output:
        for sample in range(48000 * seconds):
            value = round(4500 * math.sin(2 * math.pi * frequency * sample / 48000))
            output.write(struct.pack("<hh", value, value))


def player(path):
    return ["paplay", "--raw", "--format=s16le", "--rate=48000", "--channels=2", path]


def foreign_player(path):
    # Detach a simulated game from this test's Gul parent tree, preserving the UID.
    # A fresh interpreter performs the fork, never this heartbeat-threaded test process.
    launcher = """
import os, sys
pid = os.fork()
if pid:
    print(pid, flush=True)
    os._exit(0)
with open(os.devnull, 'rb+') as null:
    for descriptor in (0, 1, 2): os.dup2(null.fileno(), descriptor)
os.execvp('paplay', ['paplay', '--raw', '--format=s16le', '--rate=48000', '--channels=2', sys.argv[1]])
"""
    return int(subprocess.check_output([sys.executable, "-c", launcher, path], timeout=3))


def private_source(sources, nonce):
    # PulseAudio may rename the source when its name collides with the private sink.
    # Match the same unique device description as the renderer, never a sink monitor.
    label = "Gul-Screen-Audio-" + nonce
    matches = [source["name"] for source in sources
               if source.get("properties", {}).get("device.description") == label
               and source.get("monitor_of_sink_name") is None and source.get("name")]
    assert len(matches) == 1, "Expected one private, non-monitor screen audio source"
    return matches[0]


def record(source, seconds=1):
    process = subprocess.Popen(
        ["parec", "--raw", "--format=s16le", "--rate=48000", "--channels=2", "--device=" + source],
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )
    try:
        data = process.stdout.read(48000 * seconds * 4)
        assert len(data) == 48000 * seconds * 4, "PulseAudio returned incomplete capture PCM"
        return struct.unpack("<" + "h" * (len(data) // 2), data)[::2]
    finally:
        process.terminate()
        process.wait(timeout=3)


def amplitude(samples, frequency):
    cosine = sum(value * math.cos(2 * math.pi * frequency * i / 48000) for i, value in enumerate(samples))
    sine = sum(value * math.sin(2 * math.pi * frequency * i / 48000) for i, value in enumerate(samples))
    return 2 * math.hypot(cosine, sine) / max(1, len(samples)) / 32768


def start_helper(executable, nonce):
    process = subprocess.Popen([executable, "--capture", nonce], stdin=subprocess.PIPE,
                               stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    done = threading.Event()

    def heartbeat():
        while not done.wait(0.3):
            try:
                process.stdin.write(b"PING\n")
                process.stdin.flush()
            except (OSError, ValueError):
                return

    threading.Thread(target=heartbeat, daemon=True).start()
    if not select.select([process.stdout], [], [], 8)[0] or process.stdout.readline() != b"READY\n":
        done.set()
        process.kill()
        process.wait()
        raise AssertionError("Native audio helper did not become ready")
    return process, done


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--helper", required=True)
    arguments = parser.parse_args()
    server = next(line.split(": ", 1)[1] for line in pulse("info").splitlines() if line.startswith("Server String: "))
    assert server.startswith("/"), "The test requires one local Unix socket"
    os.environ["PULSE_SERVER"] = "unix:" + server
    modules = []
    players = []
    own = helper = done = None
    nonce = secrets.token_hex(16)
    old_sink = pulse("get-default-sink")
    old_source = pulse("get-default-source")
    hardware = "gul_test_hardware_" + nonce
    private = "gul_share_" + nonce
    try:
        modules.append(pulse("load-module", "module-null-sink", "sink_name=" + hardware,
                             "channels=2", "rate=48000"))
        pulse("set-default-sink", hardware)
        pulse("set-default-source", hardware + ".monitor")
        with tempfile.TemporaryDirectory(prefix="gul-audio-test-") as directory:
            for name, frequency in [("game", 440), ("gul", 880), ("new-game", 660)]:
                pcm(os.path.join(directory, name + ".pcm"), frequency)
            players.append(foreign_player(os.path.join(directory, "game.pcm")))
            own = subprocess.Popen(player(os.path.join(directory, "gul.pcm")), stderr=subprocess.DEVNULL)
            helper, done = start_helper(arguments.helper, nonce)
            time.sleep(0.6)
            assert pulse("get-default-sink") == hardware, "Capture changed the output device"
            assert pulse("get-default-source") == hardware + ".monitor", "Capture changed the microphone"
            device = private_source(json.loads(pulse("--format=json", "list", "sources")), nonce)
            shared = record(device)
            audible = record(hardware + ".monitor")
            game, gul = amplitude(shared, 440), amplitude(shared, 880)
            assert game > 0.04 and gul < game / 100, (game, gul)
            assert amplitude(audible, 440) > 0.04 and amplitude(audible, 880) > 0.04, "Callers were muted or rerouted"
            players.append(foreign_player(os.path.join(directory, "new-game.pcm")))
            time.sleep(0.4)
            added = record(device)
            assert amplitude(added, 660) > 0.04, "New playback streams were not captured"
            assert amplitude(added, 880) < 0.0005, "Gul leaked when another stream appeared"
            done.set()
            helper.stdin.close()
            assert helper.wait(timeout=4) == 0, "Orderly helper shutdown failed"
            assert private not in pulse("list", "short", "sources"), "Virtual input survived shutdown"
            helper, done = start_helper(arguments.helper, nonce)
            done.set()
            helper.kill()
            helper.wait(timeout=3)
            subprocess.run([arguments.helper, "--cleanup", nonce], check=True, timeout=4,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            assert private not in pulse("list", "short", "sources"), "Crash cleanup left a virtual device"
            print(json.dumps({"privateGame": round(game, 4), "privateGul": round(gul, 6),
                              "audibleCallers": True, "newStreams": True, "cleanup": True}))
    finally:
        if done:
            done.set()
        if helper and helper.poll() is None:
            helper.terminate()
            helper.wait(timeout=4)
        if own:
            own.terminate()
            own.wait(timeout=3)
        for pid in players:
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        for module in reversed(modules):
            pulse("unload-module", module)
        # PulseAudio's automatic null sink can disappear while the temporary sink exists.
        # Restore after unloading, without hiding the original assertion if it was transient.
        for command, name in [("set-default-sink", old_sink), ("set-default-source", old_source)]:
            subprocess.run(["pactl", command, name], check=False,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
