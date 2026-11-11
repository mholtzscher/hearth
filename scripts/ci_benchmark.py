"""Disposable CI measurement; no production cache writes or application changes."""

import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import time


ROOT = Path(".data/ci-benchmark")
EVIDENCE = ROOT / "evidence"
SNAPSHOT = ROOT / "snapshot"
TAGS = {"unit": "", "integration": "integration", "e2e": "e2e"}


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def environment():
    return {
        "revision": output("git", "rev-parse", "HEAD"),
        "go": output("go", "version"),
        "mise": output("mise", "version"),
        "node": output("node", "--version"),
        "pnpm": output("pnpm", "--version"),
        "image_os": os.environ.get("ImageOS"),
        "image_version": os.environ.get("ImageVersion"),
        "runner_arch": os.environ.get("RUNNER_ARCH"),
        "cpus": output("nproc"),
        "cpu_model": next((line.split(":", 1)[1].strip() for line in
                           Path("/proc/cpuinfo").read_text().splitlines()
                           if line.startswith("model name")), "unknown"),
        "kernel": platform.release(),
        "load": os.getloadavg(),
        "flags": {key: os.environ.get(key) for key in
                  ("CI", "GOMAXPROCS", "GOFLAGS", "MISE_JOBS")},
    }


def events(log):
    records = []
    for line in log.read_text().splitlines():
        # Combined mise tasks prefix their lines; standalone tasks do not.
        start = line.find('{"Time":')
        if start >= 0:
            records.append(json.loads(line[start:]))
    runs = sorted({(item["Package"], item["Test"]) for item in records
                   if item.get("Action") == "run" and item.get("Test")
                   and "/" not in item["Test"]})
    return {
        "top_level_tests": runs,
        "test_run_events": sum(item.get("Action") == "run" for item in records),
        "test_outcomes": sorted((item["Package"], item["Test"], item["Action"])
                                for item in records if item.get("Test") and
                                item.get("Action") in ("pass", "skip", "fail")),
        "failures": [item for item in records if item.get("Action") == "fail"],
        "packages": [item for item in records if item.get("Action") in
                     ("pass", "fail", "skip") and not item.get("Test")],
        "cached_packages": sum("(cached)" in item.get("Output", "")
                               for item in records if not item.get("Test")),
    }


def run(lane, command):
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    log = EVIDENCE / f"{lane}.log"
    data = {"lane": lane, "command": command, "environment": environment(),
            "profile": os.environ.get("BENCH_PROFILE") == "true"}
    if (SNAPSHOT / "SHA256SUMS").exists():
        data["snapshot_sha256"] = hashlib.sha256((SNAPSHOT / "SHA256SUMS").read_bytes()).hexdigest()
        data["frontend"] = json.loads((SNAPSHOT / "frontend.json").read_text())
    if data["profile"]:
        command = ["/usr/bin/time", "-v", "-o",
                   str(EVIDENCE / f"{lane}.resources.txt"), *command]
    print(f"Starting {lane}: {command}", flush=True)
    start = time.monotonic()
    with log.open("w") as stream:
        result = subprocess.run(command, stdout=stream, stderr=subprocess.STDOUT)
    data.update(seconds=time.monotonic() - start, exit_code=result.returncode,
                load_after=os.getloadavg(), **events(log))
    (EVIDENCE / f"{lane}.json").write_text(json.dumps(data, indent=2) + "\n")
    print(f"Finished {lane}: {data['seconds']:.3f}s, exit={result.returncode}, "
          f"tests={len(data['top_level_tests'])}, cached={data['cached_packages']}",
          flush=True)
    if result.returncode:
        print("\n".join(log.read_text().splitlines()[-60:]), flush=True)
        raise SystemExit(result.returncode)
    if lane in {"combined", *TAGS}:
        assert data["top_level_tests"], "No actual tests were observed"
        assert not data["cached_packages"] and not data["failures"]
    return data


def mise(task, *args):
    return ["mise", "run", "--output=interleave", task, *args]


def seed():
    run("seed-combined", mise("ci-benchmark-combined"))
    for lane, tags in TAGS.items():
        run(f"seed-{lane}", mise("test", "--", f"-tags={tags}",
                                 "-run=^$", "-count=1", "-json"))
    subprocess.run(["git", "diff", "--exit-code"], check=True)
    SNAPSHOT.mkdir(parents=True, exist_ok=True)
    dockerfile = Path("web/Dockerfile").read_text()
    images = {}
    for line in dockerfile.splitlines():
        if line.startswith("FROM "):
            image = line.split()[1]
            manifest = json.loads(output("docker", "buildx", "imagetools", "inspect",
                                         image, "--format", "{{json .Manifest}}"))
            images[image] = manifest["digest"]
            dockerfile = dockerfile.replace(f"FROM {image}", f"FROM {image}@{manifest['digest']}")
    versions = json.loads(output("npm", "view", "pnpm@12", "version", "--json"))
    pnpm = versions[-1] if isinstance(versions, list) else versions
    dockerfile = dockerfile.replace("npm install -g pnpm@12", f"npm install -g pnpm@{pnpm}")
    (SNAPSHOT / "Dockerfile.frozen").write_text(dockerfile)
    (SNAPSHOT / "frontend.json").write_text(json.dumps({"images": images, "docker_pnpm": pnpm}, indent=2))
    (SNAPSHOT / "environment.json").write_text(json.dumps(environment(), indent=2))
    for name, variable in (("build", "GOCACHE"), ("modules", "GOMODCACHE")):
        cache = output("go", "env", variable)
        subprocess.run(["tar", "-I", "zstd -T2 -3", "-cf",
                        str(SNAPSHOT / f"{name}.tar.zst"), "-C", cache, "."],
                       check=True)
    sums = []
    for path in sorted(SNAPSHOT.iterdir()):
        with path.open("rb") as stream:
            digest = hashlib.file_digest(stream, "sha256").hexdigest()
        sums.append(f"{digest}  {path.name}\n")
    (SNAPSHOT / "SHA256SUMS").write_text("".join(sums))


def verify():
    for line in (SNAPSHOT / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split("  ", 1)
        if name.endswith(".tar.zst"):
            continue
        assert hashlib.sha256((SNAPSHOT / name).read_bytes()).hexdigest() == digest
    frozen = json.loads((SNAPSHOT / "environment.json").read_text())
    current = environment()
    for key in ("revision", "go", "mise", "node", "pnpm", "image_os", "image_version",
                "runner_arch", "cpus", "cpu_model"):
        assert frozen[key] == current[key], f"Snapshot environment mismatch: {key}"


def restore():
    subprocess.run(["sha256sum", "--check", "SHA256SUMS"], cwd=SNAPSHOT, check=True)
    verify()
    for name, variable in (("build", "GOCACHE"), ("modules", "GOMODCACHE")):
        cache = Path(output("go", "env", variable))
        cache.mkdir(parents=True, exist_ok=True)
        subprocess.run(["tar", "--zstd", "-xf", str(SNAPSHOT / f"{name}.tar.zst"),
                        "-C", str(cache)], check=True)


def main():
    lane = sys.argv[1]
    if lane == "seed":
        seed()
    elif lane == "restore":
        restore()
    elif lane == "verify":
        verify()
    elif lane == "combined":
        run(lane, mise("ci-benchmark-combined"))
    elif lane == "checks":
        run(lane, mise("validate-backend-checks"))
    elif lane in TAGS:
        run(lane, mise("test", "--", f"-tags={TAGS[lane]}", "-count=1", "-json"))
    elif lane == "web":
        run(lane, ["bash", "-ec", "mise run web-test && "
                   f"docker build -f {SNAPSHOT}/Dockerfile.frozen . --tag hearth-web:ci"])
    else:
        raise SystemExit(f"Unknown benchmark lane: {lane}")


if __name__ == "__main__":
    main()
