"""Build the original and short storage commands without a workspace.

Only swarm flag parsing is executed. Redis-backed example bodies are compiled
but never run against an existing Redis installation.
"""
from pathlib import Path
import os
import subprocess
import tempfile


REPO = Path(__file__).resolve().parents[1]
COMMANDS = (
    "multi_redis", "multi_redis_url", "swarm_clustered",
)
env = os.environ.copy()
env["GOWORK"] = "off"


def run(binary, directory, *args):
    directory.mkdir(parents=True, exist_ok=True)
    # Equal argv[0] makes flag usage text comparable without hiding other output.
    return subprocess.run(
        ["storage-example", *args], executable=str(binary), cwd=directory,
        env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120,
    )


def comparable(result):
    return result.returncode, result.stdout, result.stderr


with tempfile.TemporaryDirectory(prefix="joltrin-redis-commands-") as temporary:
    work = Path(temporary)
    packages = ["./" + prefix + name for name in COMMANDS for prefix in ("examples/", "")]
    subprocess.run(["go", "build", *packages], cwd=REPO, env=env, check=True, timeout=180)
    print("Built all old/new package paths with GOWORK=off", flush=True)
    binaries = {}
    for name in COMMANDS:
        binaries[name] = {}
        for variant, source in (("original", "examples/" + name), ("short", name)):
            binary = work / "bin" / variant / name / "command"
            binary.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(
                ["go", "build", "-o", str(binary), "./" + source + "/main.go"],
                cwd=REPO, env=env, check=True, timeout=180,
            )
            binaries[name][variant] = binary
        print(f"Built both entrypoints: {name}", flush=True)

    for args, expected in ((("-h",), 0), (("-unknown-storage-flag",), 2)):
        results = [run(binaries["swarm_clustered"][variant],
                       work / "flags" / variant / str(expected), *args)
                   for variant in ("original", "short")]
        assert comparable(results[0]) == comparable(results[1]), (
            args, comparable(results[0]), comparable(results[1]),
        )
        assert results[0].returncode == expected
        assert "maxJitter" in results[0].stderr and "reverse" in results[0].stderr
    assert not list((work / "flags").rglob("data")), "flag parsing must not start database work"
    print("Swarm help and invalid-flag output/exits match without starting Redis work", flush=True)
    print("Redis command runtime was not exercised; no Redis service was started or contacted", flush=True)
