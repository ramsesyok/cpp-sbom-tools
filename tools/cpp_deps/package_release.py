"""Create one release ZIP per platform from a verified dist directory."""

import argparse
import hashlib
import subprocess
import sys
import zipfile
from pathlib import Path


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("version")
    args = parser.parse_args()
    if not args.version.startswith("v"):
        parser.error("version must start with v")

    root = Path(__file__).resolve().parents[2]
    dist = Path(__file__).resolve().parent / "dist"
    pdf = root / "docs" / "procedure" / "sbom-procedure.pdf"
    notes = root / "docs" / "releases" / f"{args.version}.md"
    license_file = root / "LICENSE"
    if not license_file.is_file():
        parser.error("repository LICENSE is missing")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    if subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip():
        parser.error("working tree must be clean")

    platforms = {
        "linux-amd64": "",
        "linux-arm64": "",
        "windows-amd64": ".exe",
    }
    for platform, suffix in platforms.items():
        executables = {f"cpp_deps-{platform}{suffix}", f"cpp_sbom-{platform}{suffix}"}
        members = [
            (dist / f"cpp_deps-{platform}{suffix}", f"cpp_deps-{platform}{suffix}"),
            (dist / f"cpp_sbom-{platform}{suffix}", f"cpp_sbom-{platform}{suffix}"),
            (dist / f"cpp_deps-{platform}.cdx.json", f"cpp_deps-{platform}.cdx.json"),
            (dist / f"cpp_sbom-{platform}.cdx.json", f"cpp_sbom-{platform}.cdx.json"),
            (dist / "THIRD_PARTY_LICENSES.txt", "THIRD_PARTY_LICENSES.txt"),
            (pdf, "sbom-procedure.pdf"),
            (notes, "RELEASE_NOTES.md"),
        ]
        if license_file.is_file():
            members.append((license_file, "LICENSE"))
        for source, _ in members:
            if not source.is_file():
                parser.error(f"missing: {source}")
        for source, name in members:
            if name not in executables:
                continue
            build = subprocess.check_output(["go", "version", "-m", str(source)], text=True)
            if f"vcs.revision={revision}" not in build or "vcs.modified=false" not in build:
                parser.error(f"stale or modified build: {source}")
        manifest = "".join(f"{sha256(source)}  {name}\n" for source, name in members)
        archive = dist / f"cpp-sbom-toolkit-{args.version}-{platform}.zip"
        with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as out:
            for source, name in members:
                info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.external_attr = (0o755 if name in executables else 0o644) << 16
                out.writestr(info, source.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
            out.writestr("SHA256SUMS", manifest)
            out.writestr("SOURCE_COMMIT.txt", revision + "\n")
        print(f"{archive.name}: {archive.stat().st_size} bytes")
    return 0


if __name__ == "__main__":
    sys.exit(main())
