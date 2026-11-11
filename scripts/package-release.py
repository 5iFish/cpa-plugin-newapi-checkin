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
PLATFORMS = ("windows-amd64", "windows-arm64", "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64")


def library_name(platform):
    if platform not in PLATFORMS:
        raise ValueError(f"Unsupported platform: {platform}")
    goos, _ = platform.split("-")
    return "newapi-checkin" + {"windows": ".dll", "linux": ".so", "darwin": ".dylib"}[goos]


def validate_binary(data, platform):
    library_name(platform)
    if platform.startswith("windows-"):
        if data[:2] != b"MZ" or len(data) < 64:
            raise ValueError("Expected a Windows PE library")
        offset = struct.unpack_from("<I", data, 0x3C)[0]
        if offset + 24 > len(data) or data[offset:offset + 4] != b"PE\0\0":
            raise ValueError("Invalid PE header")
        machine = struct.unpack_from("<H", data, offset + 4)[0]
        characteristics = struct.unpack_from("<H", data, offset + 22)[0]
        expected = 0x8664 if platform == "windows-amd64" else 0xAA64
        if machine != expected or not characteristics & 0x2000:
            raise ValueError("PE type/architecture does not match requested platform")
    elif platform.startswith("darwin-"):
        if len(data) < 32 or data[:4] != b"\xcf\xfa\xed\xfe":
            raise ValueError("Expected a 64-bit little-endian Mach-O library")
        machine, _, kind, ncmds, sizeofcmds = struct.unpack_from("<IIIII", data, 4)
        expected = 0x01000007 if platform == "darwin-amd64" else 0x0100000C
        if kind != 6 or machine != expected:
            raise ValueError("Mach-O type/architecture does not match requested platform")
        if 32 + sizeofcmds > len(data) or ncmds * 8 > sizeofcmds:
            raise ValueError("Truncated Mach-O load commands")
    else:
        if len(data) < 64 or data[:6] != b"\x7fELF\x02\x01":
            raise ValueError("Expected a 64-bit little-endian ELF library")
        kind, machine = struct.unpack_from("<HH", data, 16)
        expected = 62 if platform == "linux-amd64" else 183
        if kind != 3 or machine != expected:
            raise ValueError("ELF type/architecture does not match requested platform")


def read_version(root):
    version = (root / "VERSION").read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("VERSION must be a stable major.minor.patch version")
    return version


def write_checksums(folder, archives):
    lines = [f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in sorted(archives)]
    output = folder / "checksums.txt"
    output.write_text("".join(lines), encoding="utf-8", newline="\n")
    return output


def collect_release_bundle(root, version):
    folder = root / version
    expected = {f"newapi-checkin_{version}_{platform.replace('-', '_')}.zip" for platform in PLATFORMS}
    actual = {path.name for path in folder.glob("*.zip")}
    if actual != expected:
        raise ValueError(f"Incomplete release assets: expected {sorted(expected)}, got {sorted(actual)}")
    return write_checksums(folder, [folder / name for name in expected])


def package_archive(root, platform):
    version = read_version(root)
    name_of_library = library_name(platform)
    goos, arch = platform.split("-")
    data = (root / "dist" / platform / name_of_library).read_bytes()
    validate_binary(data, platform)
    outdir = root / "dist" / "release" / version
    outdir.mkdir(parents=True, exist_ok=True)
    archive = outdir / f"newapi-checkin_{version}_{goos}_{arch}.zip"
    entries = {name_of_library: data}
    for name in ("README.md", "README.en.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md", "docs/compatibility.md"):
        entries[name] = (root / name).read_bytes()
    entries["manifest.json"] = (json.dumps({
        "id": "newapi-checkin", "version": version, "goos": goos,
        "goarch": arch, "library": name_of_library,
        "library_sha256": hashlib.sha256(data).hexdigest(),
    }, indent=2) + "\n").encode()
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for name, content in sorted(entries.items()):
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 9, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 3
            info.external_attr = (0o100755 if name == name_of_library else 0o100644) << 16
            output.writestr(info, content, compresslevel=9)
    with zipfile.ZipFile(archive) as check:
        if check.testzip() is not None:
            raise RuntimeError("Archive integrity check failed")
        if check.read(name_of_library) != data:
            raise RuntimeError("Packaged binary differs from build output")
    write_checksums(outdir, outdir.glob(f"newapi-checkin_{version}_*.zip"))
    return archive


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--platform", choices=PLATFORMS)
    mode.add_argument("--bundle-dir", type=Path, help="Verify a downloaded six-platform artifact bundle")
    args = parser.parse_args()
    try:
        if args.bundle_dir is not None:
            checksums = collect_release_bundle(args.bundle_dir, read_version(ROOT))
            print(json.dumps({"checksums": str(checksums)}))
        else:
            archive = package_archive(ROOT, args.platform)
            print(json.dumps({"archive": str(archive), "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "bytes": archive.stat().st_size}))
    except (ValueError, OSError) as error:
        parser.exit(1, f"{error}\n")


if __name__ == "__main__":
    main()
