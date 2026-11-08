#!/usr/bin/env python3
"""Package native libraries using CPA's release asset naming contract."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import struct
import zipfile

ROOT = Path(__file__).resolve().parent.parent
PLATFORMS = ("windows-amd64", "linux-amd64", "linux-arm64")


def validate_binary(data, platform):
    if platform == "windows-amd64":
        if data[:2] != b"MZ" or len(data) < 64:
            raise ValueError("Expected a Windows PE library")
        offset = struct.unpack_from("<I", data, 0x3C)[0]
        if offset + 24 > len(data) or data[offset:offset + 4] != b"PE\0\0":
            raise ValueError("Invalid PE header")
        machine = struct.unpack_from("<H", data, offset + 4)[0]
        characteristics = struct.unpack_from("<H", data, offset + 22)[0]
        if machine != 0x8664 or not characteristics & 0x2000:
            raise ValueError("Expected an amd64 DLL")
    else:
        if len(data) < 64 or data[:6] != b"\x7fELF\x02\x01":
            raise ValueError("Expected a 64-bit little-endian ELF library")
        kind, machine = struct.unpack_from("<HH", data, 16)
        expected = 62 if platform == "linux-amd64" else 183
        if kind != 3 or machine != expected:
            raise ValueError("ELF type/architecture does not match requested platform")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=PLATFORMS, required=True)
    args = parser.parse_args()
    version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise SystemExit("VERSION must be a stable major.minor.patch version")
    goos, arch = args.platform.split("-")
    library_name = "newapi-checkin" + (".dll" if goos == "windows" else ".so")
    data = (ROOT / "dist" / args.platform / library_name).read_bytes()
    validate_binary(data, args.platform)
    outdir = ROOT / "dist" / "release" / version
    outdir.mkdir(parents=True, exist_ok=True)
    archive = outdir / f"newapi-checkin_{version}_{goos}_{arch}.zip"
    entries = {library_name: data}
    for name in ("README.md", "README.en.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md", "docs/compatibility.md"):
        entries[name] = (ROOT / name).read_bytes()
    entries["manifest.json"] = (json.dumps({
        "id": "newapi-checkin", "version": version, "goos": goos,
        "goarch": arch, "library": library_name,
        "library_sha256": hashlib.sha256(data).hexdigest(),
    }, indent=2) + "\n").encode()
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for name, content in sorted(entries.items()):
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 9, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 3
            info.external_attr = (0o100755 if name == library_name else 0o100644) << 16
            output.writestr(info, content, compresslevel=9)
    with zipfile.ZipFile(archive) as check:
        if check.testzip() is not None:
            raise RuntimeError("Archive integrity check failed")
        if check.read(library_name) != data:
            raise RuntimeError("Packaged binary differs from build output")
    sums = []
    for item in sorted(outdir.glob(f"newapi-checkin_{version}_*.zip")):
        sums.append(f"{hashlib.sha256(item.read_bytes()).hexdigest()}  {item.name}\n")
    (outdir / "checksums.txt").write_text("".join(sums), encoding="utf-8", newline="\n")
    print(json.dumps({"archive": str(archive), "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "bytes": archive.stat().st_size}))


if __name__ == "__main__":
    main()
