#!/usr/bin/env python3
"""Cross-compile standalone binaries and package their license notices."""

import argparse
import hashlib
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
TARGETS = [(system, arch) for system in ("linux", "darwin", "windows")
           for arch in ("amd64", "arm64")]
DOCUMENTS = ("LICENSE", "THIRD_PARTY_NOTICES.md", "README.md", "USAGE.md",
             "docs/images/fine-grained-token-permissions.png")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="Release tag, e.g. v5.0.0")
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    parser.add_argument("--target", action="append", help="Optional OS/arch subset")
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?", args.version):
        parser.error("version must be a full vMAJOR.MINOR.PATCH tag, optionally with a prerelease suffix")
    targets = TARGETS
    if args.target:
        targets = []
        for value in args.target:
            parts = tuple(value.split("/"))
            if parts not in TARGETS:
                parser.error(f"unsupported target: {value}")
            if parts not in targets:
                targets.append(parts)
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    archives = []
    for system, arch in targets:
        binary_name = "drupal-update.exe" if system == "windows" else "drupal-update"
        suffix = ".zip" if system == "windows" else ".tar.gz"
        archive = output / f"drupal-update_{args.version}_{system}_{arch}{suffix}"
        if archive.exists():
            parser.error(f"archive already exists: {archive}; use a fresh output directory")
        with tempfile.TemporaryDirectory(prefix="drupal-update-release-") as temp:
            binary = Path(temp) / binary_name
            env = dict(os.environ, CGO_ENABLED="0", GOOS=system, GOARCH=arch)
            subprocess.run([
                "go", "build", "-trimpath", "-ldflags",
                f"-s -w -X main.buildVersion={args.version}", "-o", str(binary), ".",
            ], cwd=ROOT, env=env, check=True)
            wrapper = Path(temp) / "drupal-update.sh"
            wrapper.write_text((ROOT / "drupal-update.sh").read_text().replace(
                "DEFAULT_RELEASE=latest", f"DEFAULT_RELEASE={args.version}"), encoding="utf-8")
            wrapper.chmod(0o755)
            files = [(binary, binary_name), (wrapper, "drupal-update.sh")] + [(ROOT / name, name) for name in DOCUMENTS]
            files += [(path, path.relative_to(ROOT).as_posix())
                      for path in sorted((ROOT / "examples").glob("*.yml"))]
            if system == "windows":
                with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as handle:
                    for source, name in files:
                        handle.write(source, name)
            else:
                with tarfile.open(archive, "w:gz") as handle:
                    for source, name in files:
                        handle.add(source, arcname=name, recursive=False)
        archives.append(archive)
        print(archive.name, flush=True)
    checksums = [f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
                 for path in sorted(archives)]
    (output / "SHA256SUMS").write_text("".join(checksums), encoding="utf-8")
    print("SHA256SUMS", flush=True)


if __name__ == "__main__":
    main()
