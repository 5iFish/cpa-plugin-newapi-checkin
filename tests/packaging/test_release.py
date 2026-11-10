import importlib.util
import hashlib
import json
import struct
import tempfile
import unittest
import zipfile
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "package-release.py"
SPEC = importlib.util.spec_from_file_location("package_release", SCRIPT)
PACKAGE_RELEASE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(PACKAGE_RELEASE)


def macho64(cputype: int, filetype: int = 6) -> bytes:
    header = struct.pack("<IiiIIII", 0xFEEDFACF, cputype, 0, filetype, 0, 0, 0)
    return header + bytes(64 - len(header))


class ReleasePackagingTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix="newapi-package-"))

    def test_darwin_architectures_and_library_suffix(self):
        self.assertEqual(PACKAGE_RELEASE.library_name("darwin-amd64"), "newapi-checkin.dylib")
        self.assertEqual(PACKAGE_RELEASE.library_name("darwin-arm64"), "newapi-checkin.dylib")
        PACKAGE_RELEASE.validate_binary(macho64(0x01000007), "darwin-amd64")
        PACKAGE_RELEASE.validate_binary(macho64(0x0100000C), "darwin-arm64")

    def test_darwin_validation_rejects_wrong_architecture_filetype_and_truncation(self):
        with self.assertRaises(ValueError):
            PACKAGE_RELEASE.validate_binary(macho64(0x0100000C), "darwin-amd64")
        with self.assertRaises(ValueError):
            PACKAGE_RELEASE.validate_binary(macho64(0x01000007, filetype=2), "darwin-amd64")
        with self.assertRaises(ValueError):
            PACKAGE_RELEASE.validate_binary(b"\xCF\xFA\xED\xFE", "darwin-amd64")

    def test_package_contains_dylib_and_manifest(self):
        for platform, cpu in (("darwin-amd64", 0x01000007), ("darwin-arm64", 0x0100000C)):
            root = self.root
            (root / "VERSION").write_text("0.1.1\n", encoding="utf-8")
            for name in ("README.md", "README.en.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "CHANGELOG.md"):
                (root / name).write_text(name, encoding="utf-8")
            (root / "docs").mkdir(exist_ok=True)
            (root / "docs" / "compatibility.md").write_text("compatibility", encoding="utf-8")
            binary = macho64(cpu)
            output = root / "dist" / platform / "newapi-checkin.dylib"
            output.parent.mkdir(parents=True)
            output.write_bytes(binary)

            archive = PACKAGE_RELEASE.package_archive(root, platform)

            self.assertEqual(archive.name, "newapi-checkin_0.1.1_" + platform.replace("-", "_") + ".zip")
            with zipfile.ZipFile(archive) as bundle:
                self.assertEqual(bundle.testzip(), None)
                self.assertEqual(bundle.read("newapi-checkin.dylib"), binary)
                manifest = json.loads(bundle.read("manifest.json"))
                self.assertEqual(manifest["goos"], "darwin")
                self.assertEqual(manifest["goarch"], platform.split("-")[1])
                self.assertEqual(manifest["library"], "newapi-checkin.dylib")
                self.assertEqual(manifest["library_sha256"], hashlib.sha256(binary).hexdigest())
                self.assertEqual([n for n in bundle.namelist() if n.endswith((".so", ".dll", ".dylib"))], ["newapi-checkin.dylib"])

    def test_collect_release_requires_exact_five_platforms(self):
        with self.subTest("complete and missing"):
            root = self.root
            version_dir = root / "0.1.1"
            version_dir.mkdir()
            expected = {
                "newapi-checkin_0.1.1_windows_amd64.zip",
                "newapi-checkin_0.1.1_linux_amd64.zip",
                "newapi-checkin_0.1.1_linux_arm64.zip",
                "newapi-checkin_0.1.1_darwin_amd64.zip",
                "newapi-checkin_0.1.1_darwin_arm64.zip",
            }
            for name in expected:
                (version_dir / name).write_bytes(name.encode("utf-8"))

            checksums = PACKAGE_RELEASE.collect_release_bundle(root, "0.1.1")

            self.assertEqual(checksums, version_dir / "checksums.txt")
            lines = checksums.read_text(encoding="utf-8").splitlines()
            self.assertEqual(len(lines), 5)
            self.assertEqual(
                {line.split("  ", 1)[1] for line in lines},
                expected,
            )
            for line in lines:
                digest, name = line.split("  ", 1)
                self.assertEqual(digest, hashlib.sha256((version_dir / name).read_bytes()).hexdigest())

            missing = version_dir / "newapi-checkin_0.1.1_darwin_arm64.zip"
            missing.rename(missing.with_suffix(".pending"))
            with self.assertRaises(ValueError):
                PACKAGE_RELEASE.collect_release_bundle(root, "0.1.1")

            missing.with_suffix(".pending").rename(missing)
            (version_dir / "newapi-checkin_0.1.0_linux_amd64.zip").write_bytes(b"old version")
            with self.assertRaises(ValueError):
                PACKAGE_RELEASE.collect_release_bundle(root, "0.1.1")


if __name__ == "__main__":
    unittest.main()
