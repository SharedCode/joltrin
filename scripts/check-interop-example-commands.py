"""Build and exercise original and short standalone interop commands without a workspace.
"""
from pathlib import Path
import os
import re
import subprocess
import tempfile


REPO = Path(__file__).resolve().parents[1]
COMMANDS = (
    "interop_indexes", "interop_indexes/struct_key_example", "interop_jsondb",
)
UUID = re.compile(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}")
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
    return result.returncode, UUID.sub("<uuid>", result.stdout), result.stderr


with tempfile.TemporaryDirectory(prefix="joltrin-interop-commands-") as temporary:
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

    for name in COMMANDS[:3]:
        results = [run(binaries[name][variant], work / "runs" / variant / name)
                   for variant in ("original", "short")]
        assert results[0].returncode == 0, (name, results[0].stdout, results[0].stderr)
        assert comparable(results[0]) == comparable(results[1]), (
            name, comparable(results[0]), comparable(results[1]),
        )
        output = results[1].stdout
        if name == "interop_jsondb":
            assert "Wrote 2 people" in output and "FirstName:John LastName:Doe Age:30" in output
        else:
            row = (r"^Key:\s+(\w+)\s+\| Price:\s+([\d.]+)" if name == "interop_indexes"
                   else r"^Category:\s+(\w+)\s+\| Price:\s+([\d.]+)")
            keys = [(category, -float(price)) for category, price in re.findall(row, output, re.M)]
            assert len(keys) == 500 and keys == sorted(keys), (name, len(keys))
        print(f"Standalone round-trip output and ordering match: {name}", flush=True)
