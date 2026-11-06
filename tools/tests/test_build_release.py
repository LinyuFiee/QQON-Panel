"""Isolated release tests: inert scripts and ELF/PE-shaped files, never installers."""

import gzip
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile


TOOL = Path(__file__).resolve().parents[1] / "build_release.py"
SPEC = importlib.util.spec_from_file_location("panel_build_release", TOOL)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)

TAG = "v0.5.0"
REPOSITORY = "example-owner/example-repo"
# Deliberate unit-test identity, not a real repository commit or release.
COMMIT = "a" * 40
BUILD_TIME = "2026-11-06T00:00:00Z"
ASSETS = (
    "panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "panel-windows-amd64.zip",
    "install.sh", "update.sh", "install.ps1", "update.ps1", "release.json", "SHA256SUMS",
)


def elf_fixture(arch, program_types=(1,)):
    """Small non-executable ELF-shaped data for testing package validation."""
    ident = b"\x7fELF\x02\x01\x01" + b"\0" * 9
    header = struct.pack(
        "<16sHHIQQQIHHHHHH", ident, 2, {"amd64": 62, "arm64": 183}[arch],
        1, 0, 64, 0, 0, 64, 56, len(program_types), 0, 0, 0,
    )
    headers = b"".join(
        struct.pack("<IIQQQQQQ", kind, 0, 0, 0, 0, 0, 0, 0)
        for kind in program_types
    )
    return header + headers + b"fixture only; not executable\n"


def pe_fixture(machine=0x8664, magic=0x20b, flags=0x22):
    """PE-shaped inert bytes, not a runnable program or a real repository release."""
    dos = bytearray(64)
    dos[:2] = b"MZ"
    struct.pack_into("<I", dos, 60, 64)
    coff = b"PE\0\0" + struct.pack("<HHIIIHH", machine, 1, 0, 0, 0, 112, flags)
    optional = struct.pack("<H", magic) + b"\0" * 110
    section = bytearray(40)
    section[:5] = b".text"
    struct.pack_into("<II", section, 16, 4, 240)
    return bytes(dos) + coff + optional + bytes(section) + b"TEST"


def zip_fixture(data=None, name="panel.exe", mode=stat.S_IFREG | 0o755, method=zipfile.ZIP_DEFLATED,
                extra=b"", comment=b"", archive_comment=b"", extra_member=False, force_zip64=False):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w") as packed:
        member = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
        member.create_system = 3
        member.external_attr = mode << 16
        member.compress_type = method
        member.extra, member.comment = extra, comment
        with packed.open(member, "w", force_zip64=force_zip64) as stream:
            stream.write(pe_fixture() if data is None else data)
        if extra_member:
            packed.writestr("extra", b"not allowed")
        packed.comment = archive_comment
    return output.getvalue()


class IdentityTests(unittest.TestCase):
    def test_stable_versions(self):
        for tag in ("v0.0.0", TAG, "v2147483647.2147483647.2147483647"):
            with self.subTest(tag=tag):
                self.assertEqual(release.validate_version(tag), tag[1:])

    def test_reject_invalid_versions(self):
        invalid = (
            "0.5.0", "V0.5.0", "v01.2.3", "v1.02.3", "v1.2.03",
            "v1.2", "v1.2.3.4", "v1.2.3-rc.1", "v1.2.3+build",
            "v2147483648.0.0", "v1.2.3\n", " v1.2.3", "v１.2.3",
            "v1.2.3 -X main.commit=bad", "v" + "9" * 5000 + ".0.0",
        )
        for tag in invalid:
            with self.subTest(tag=tag[:80]):
                with self.assertRaises(release.ReleaseError):
                    release.validate_version(tag)

    def test_repository_rules(self):
        for repository in (REPOSITORY, "A/b", "a-b/c_d.e-f", "a" * 100 + "/" + "b" * 100):
            with self.subTest(repository=repository):
                self.assertEqual(release.validate_repository(repository), repository)
        for repository in (
            "", "owner", "https://github.com/owner/repo", "a/b/c", "a./b",
            "a_b/repo", "a.b/repo", "-a/b", "a-/b", "a/.b", "a/b.",
            "a/_b", "a/b-", "a/..", "./b", "a/b?x", "a/b#x", "a/b\n",
            "a\\b/repo", "a/中文", "a" * 101 + "/b", "a/" + "b" * 101,
        ):
            with self.subTest(repository=repository):
                with self.assertRaises(release.ReleaseError):
                    release.validate_repository(repository)

    def test_full_lowercase_commit_required(self):
        for commit in (COMMIT, "0123456789abcdef" * 4):
            self.assertEqual(release.validate_commit(commit), commit)
        for commit in ("", "unknown", "abc1234", "a" * 39, "a" * 41, "a" * 63,
                       "a" * 65, "A" * 40, "g" * 40, COMMIT + "\n", "--help"):
            with self.subTest(commit=commit):
                with self.assertRaises(release.ReleaseError):
                    release.validate_commit(commit)

    def test_exact_real_utc_timestamp(self):
        for value in (BUILD_TIME, "2024-02-29T23:59:59Z", "0001-01-01T00:00:00Z"):
            self.assertEqual(release.validate_build_time(value), value)
        for value in (
            "2026-02-29T00:00:00Z", "2026-11-31T00:00:00Z", "0000-01-01T00:00:00Z",
            "2026-11-06T24:00:00Z", "2026-11-06T00:00:60Z", "2026-11-06",
            "2026-11-06T00:00:00+00:00", "2026-11-06T00:00:00.000Z",
            "2026-11-06t00:00:00z", "2026-1-6T00:00:00Z", BUILD_TIME + "\n",
        ):
            with self.subTest(value=value):
                with self.assertRaises(release.ReleaseError):
                    release.validate_build_time(value)


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="panel-release-tests-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.project = self.root / "source"
        (self.project / "scripts").mkdir(parents=True)
        self.scripts = {
            "install.sh": b"#!/bin/bash\n# inert installer fixture\nexit 99\n",
            "update.sh": b"#!/bin/bash\n# inert updater fixture\nexit 98\n",
            "install.ps1": b"# inert Windows installer fixture, test only\nexit 99\n",
            "update.ps1": b"# inert Windows updater fixture, test only\nexit 98\n",
        }
        for name, data in self.scripts.items():
            (self.project / "scripts" / name).write_bytes(data)
        (self.project / "private-fixture.txt").write_text("must not be packaged", encoding="utf-8")
        self.output = self.root / "new-release"

    def fake_go(self, command, **kwargs):
        self.assertIs(kwargs["shell"], False)
        self.assertTrue(kwargs["check"])
        self.assertGreater(kwargs["timeout"], 0)
        self.assertEqual(Path(kwargs["cwd"]), self.project)
        self.assertEqual(command[:2], ["go", "build"])
        target_os, arch = kwargs["env"]["GOOS"], kwargs["env"]["GOARCH"]
        Path(command[command.index("-o") + 1]).write_bytes(pe_fixture() if target_os == "windows" else elf_fixture(arch))
        return subprocess.CompletedProcess(command, 0)

    def build(self, **overrides):
        parameters = dict(
            version=TAG, repository=REPOSITORY, commit=COMMIT, build_time=BUILD_TIME,
            output=self.output, project_root=self.project, go="go",
        )
        parameters.update(overrides)
        return release.build_release(**parameters)

    def verify(self, **overrides):
        parameters = dict(
            version=TAG, repository=REPOSITORY, commit=COMMIT,
            build_time=BUILD_TIME, output=self.output,
        )
        parameters.update(overrides)
        return release.verify_release(**parameters)

    def make_package(self):
        with mock.patch.object(release.subprocess, "run", side_effect=self.fake_go):
            return self.build()

    def refresh_checksums(self):
        lines = [
            f"{hashlib.sha256((self.output / name).read_bytes()).hexdigest()}  {name}\n"
            for name in ASSETS[:-1]
        ]
        (self.output / "SHA256SUMS").write_bytes("".join(lines).encode("ascii"))

    def test_exact_assets_metadata_checksums_and_build_flags(self):
        hostile = {
            "CGO_ENABLED": "1", "GOOS": "windows", "GOARCH": "386",
            "GOFLAGS": "-race -buildmode=pie", "GOENV": "custom.env",
            "GOWORK": "custom.work", "GOTOOLCHAIN": "auto",
            "GOAMD64": "v4", "GOARM64": "v9.0", "GOEXPERIMENT": "untrusted",
        }
        with mock.patch.dict(os.environ, hostile):
            with mock.patch.object(release.subprocess, "run", side_effect=self.fake_go) as runner:
                manifest = self.build()
        self.assertEqual({path.name for path in self.output.iterdir()}, set(ASSETS))
        self.assertEqual(set(manifest), {
            "format", "version", "tag", "repository", "dataSchema", "commit", "buildTime", "binaries",
        })
        self.assertEqual(manifest["format"], 3)
        self.assertEqual(manifest["dataSchema"], 1)
        self.assertEqual(manifest["version"], "0.5.0")
        self.assertEqual(manifest["tag"], TAG)
        self.assertEqual(manifest["repository"], REPOSITORY)
        self.assertEqual(manifest["commit"], COMMIT)
        self.assertEqual(manifest["buildTime"], BUILD_TIME)
        self.assertEqual(runner.call_count, 3)
        targets = (("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64"))
        for call, (target_os, arch), binary in zip(runner.call_args_list, targets, manifest["binaries"]):
            command = call.args[0]
            env = call.kwargs["env"]
            self.assertIn("-trimpath", command)
            self.assertIn("-mod=readonly", command)
            self.assertIn("-buildvcs=false", command)
            self.assertEqual(command[-1], ".")
            self.assertEqual(command[command.index("-ldflags") + 1].split(), [
                "-s", "-w", "-X", "main.version=0.5.0", "-X", f"main.commit={COMMIT}",
                "-X", f"main.buildTime={BUILD_TIME}", "-X", f"main.githubRepository={REPOSITORY}",
            ])
            for key, value in {
                "CGO_ENABLED": "0", "GOOS": target_os, "GOARCH": arch, "GOFLAGS": "",
                "GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local",
                "GOAMD64": "v1", "GOARM64": "v8.0", "GOEXPERIMENT": "",
            }.items():
                self.assertEqual(env[key], value)
            compiled = Path(command[command.index("-o") + 1])
            self.assertNotEqual(compiled.parent, self.output)
            self.assertFalse(compiled.parent.exists(), "temporary Go output must be cleaned up")
            name = f"panel-linux-{arch}.tar.gz" if target_os == "linux" else "panel-windows-amd64.zip"
            data = elf_fixture(arch) if target_os == "linux" else pe_fixture()
            archive = (self.output / name).read_bytes()
            self.assertEqual(binary, {
                "os": target_os, "arch": arch, "name": "panel" if target_os == "linux" else "panel.exe",
                "size": len(data), "sha256": hashlib.sha256(data).hexdigest(),
                "archive": {"name": name, "size": len(archive), "sha256": hashlib.sha256(archive).hexdigest()},
            })
            if target_os == "windows":
                with zipfile.ZipFile(io.BytesIO(archive)) as packed:
                    self.assertEqual(packed.namelist(), ["panel.exe"])
                    member = packed.infolist()[0]
                    self.assertEqual(member.date_time, (1980, 1, 1, 0, 0, 0))
                    self.assertEqual(member.create_system, 3)
                    self.assertEqual(member.external_attr, (stat.S_IFREG | 0o755) << 16)
                    self.assertEqual((member.extra, member.comment, packed.comment, member.flag_bits), (b"", b"", b"", 0))
                    self.assertEqual(packed.read(member), data)
                continue
            self.assertEqual(archive[:4], b"\x1f\x8b\x08\x00")
            self.assertEqual(archive[4:8], b"\0" * 4)
            with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as packed:
                members = packed.getmembers()
                self.assertEqual(len(members), 1)
                member = members[0]
                self.assertEqual((member.name, member.type, member.mode, member.uid, member.gid,
                                  member.mtime, member.uname, member.gname, member.linkname),
                                 ("panel", tarfile.REGTYPE, 0o755, 0, 0, 0, "", "", ""))
                self.assertEqual(member.pax_headers, {})
                self.assertEqual(packed.extractfile(member).read(), data)
        for name, data in self.scripts.items():
            self.assertEqual((self.output / name).read_bytes(), data)
        sums = (self.output / "SHA256SUMS").read_bytes()
        self.assertEqual(sums, "".join(
            f"{hashlib.sha256((self.output / name).read_bytes()).hexdigest()}  {name}\n"
            for name in ASSETS[:-1]
        ).encode("ascii"))
        self.assertEqual(self.verify(), manifest)

    def test_supplied_identity_is_reproducible_with_same_binary_bytes(self):
        self.make_package()
        second = self.root / "second-release"
        with mock.patch.object(release.subprocess, "run", side_effect=self.fake_go):
            self.build(output=second)
        for name in ASSETS:
            self.assertEqual((self.output / name).read_bytes(), (second / name).read_bytes())

    def test_default_build_time_is_canonical_utc(self):
        with mock.patch.object(release.subprocess, "run", side_effect=self.fake_go):
            manifest = self.build(build_time=None)
        self.assertEqual(release.validate_build_time(manifest["buildTime"]), manifest["buildTime"])
        self.assertEqual(self.verify(build_time=None), manifest)

    def test_existing_empty_or_nonempty_output_is_never_reused(self):
        self.output.mkdir()
        with mock.patch.object(release.subprocess, "run") as runner:
            with self.assertRaises(release.ReleaseError):
                self.build()
            marker = self.output / "user-file.txt"
            marker.write_bytes(b"keep")
            with self.assertRaises(release.ReleaseError):
                self.build()
            self.assertEqual(marker.read_bytes(), b"keep")
            runner.assert_not_called()

    def test_output_file_is_not_overwritten(self):
        self.output.write_bytes(b"keep")
        with self.assertRaises(release.ReleaseError):
            self.build()
        self.assertEqual(self.output.read_bytes(), b"keep")

    def test_relative_traversing_and_missing_parent_paths_are_rejected(self):
        for output in (Path("relative-release"), self.root / "source" / ".." / "new-release",
                       self.root / "missing-parent" / "release"):
            with self.subTest(output=str(output)):
                with self.assertRaises(release.ReleaseError):
                    self.build(output=output)
        self.assertFalse(self.output.exists())

    def test_missing_or_empty_script_fails_before_creating_output(self):
        script = self.project / "scripts" / "update.sh"
        for remove in (False, True):
            if remove:
                script.unlink()
            else:
                script.write_bytes(b"")
            with mock.patch.object(release.subprocess, "run") as runner:
                with self.assertRaises(release.ReleaseError):
                    self.build()
                runner.assert_not_called()
            self.assertFalse(self.output.exists())

    def test_script_size_limits_fail_before_creating_output(self):
        for name, limit in (("install.sh", 512 * 1024), ("update.sh", 32 * 1024),
                            ("install.ps1", 512 * 1024), ("update.ps1", 32 * 1024)):
            self.assertEqual(release.SCRIPT_LIMITS[name], limit)
            script = self.project / "scripts" / name
            script.write_bytes(b"x" * (limit + 1))
            with mock.patch.object(release.subprocess, "run") as runner:
                with self.assertRaises(release.ReleaseError):
                    self.build()
                runner.assert_not_called()
            self.assertFalse(self.output.exists())
            script.write_bytes(self.scripts[name])

    def test_invalid_identity_fails_before_creating_output(self):
        for override in ({"version": "v01.0.0"}, {"repository": "bad/repo/extra"},
                         {"commit": "unknown"}, {"build_time": "2026-02-30T00:00:00Z"}):
            with self.subTest(override=override):
                with mock.patch.object(release.subprocess, "run") as runner:
                    with self.assertRaises(release.ReleaseError):
                        self.build(**override)
                    runner.assert_not_called()
                self.assertFalse(self.output.exists())

    def test_failed_or_timed_out_build_has_no_complete_manifest(self):
        failures = (
            subprocess.CalledProcessError(1, ["go", "build"]),
            subprocess.TimeoutExpired(["go", "build"], 1),
            FileNotFoundError("missing Go executable"),
        )
        for index, failure in enumerate(failures):
            output = self.root / f"failed-{index}"
            with mock.patch.object(release.subprocess, "run", side_effect=failure):
                with self.assertRaises(release.ReleaseError):
                    self.build(output=output)
            self.assertFalse((output / "release.json").exists())
            self.assertFalse((output / "SHA256SUMS").exists())

    def test_reject_wrong_arch_non_elf_and_dynamic_binaries(self):
        invalid = (
            b"not an ELF binary", elf_fixture("arm64"), elf_fixture("amd64", (1, 3)),
            elf_fixture("amd64", (1, 2)), elf_fixture("amd64")[:70],
        )
        for index, data in enumerate(invalid):
            def bad_go(command, **kwargs):
                Path(command[command.index("-o") + 1]).write_bytes(data)
            output = self.root / f"invalid-binary-{index}"
            with self.subTest(index=index):
                with mock.patch.object(release.subprocess, "run", side_effect=bad_go):
                    with self.assertRaises(release.ReleaseError):
                        self.build(output=output)
                self.assertFalse((output / "release.json").exists())

    def test_binary_size_limit(self):
        with mock.patch.object(release, "MAX_BINARY_SIZE", 100):
            with mock.patch.object(release.subprocess, "run", side_effect=self.fake_go):
                with self.assertRaises(release.ReleaseError):
                    self.build()

    def test_checksums_detect_binary_script_and_manifest_tampering(self):
        self.make_package()
        for name in ASSETS[:-1]:
            path = self.output / name
            original = path.read_bytes()
            path.write_bytes(original + b" ")
            with self.subTest(name=name):
                with self.assertRaises(release.ReleaseError):
                    self.verify()
            path.write_bytes(original)
        self.verify()

    def test_verify_rejects_missing_extra_and_duplicate_checksum_entries(self):
        self.make_package()
        sums = self.output / "SHA256SUMS"
        original = sums.read_bytes()
        for invalid in (original.split(b"\n", 1)[1], original + original.splitlines(keepends=True)[0],
                        original.replace(b"  install.sh", b"  ../install.sh"), original.replace(b"\n", b"\r\n")):
            sums.write_bytes(invalid)
            with self.assertRaises(release.ReleaseError):
                self.verify()
        sums.write_bytes(original)
        extra = self.output / "unexpected.txt"
        extra.write_bytes(b"extra")
        with self.assertRaises(release.ReleaseError):
            self.verify()
        extra.unlink()
        (self.output / "install.sh").unlink()
        with self.assertRaises(release.ReleaseError):
            self.verify()

    def test_manifest_strict_fields_types_and_expected_identity(self):
        manifest = self.make_package()
        path = self.output / "release.json"
        mutations = (
            {"format": True}, {"dataSchema": True}, {"dataSchema": 2}, {"version": "0.3.1"},
            {"commit": "b" * 40}, {"buildTime": "2026-11-07T00:00:00Z"},
            {"repository": "other/repo"}, {"extra": "not allowed"}, {"binaries": []},
        )
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                modified = dict(manifest, **mutation)
                path.write_text(json.dumps(modified), encoding="utf-8")
                self.refresh_checksums()
                with self.assertRaises(release.ReleaseError):
                    self.verify()
        raw = json.dumps(manifest)
        path.write_text(raw[:-1] + ', "format": 1}', encoding="utf-8")
        self.refresh_checksums()
        with self.assertRaises(release.ReleaseError):
            self.verify()

    def test_metadata_and_checksums_size_limits(self):
        self.make_package()
        for constant in ("MAX_MANIFEST_SIZE", "MAX_CHECKSUMS_SIZE"):
            with self.subTest(constant=constant):
                with mock.patch.object(release, constant, 1):
                    with self.assertRaises(release.ReleaseError):
                        self.verify()

    def replace_archive(self, data, index=0):
        path = self.output / "release.json"
        manifest = json.loads(path.read_bytes())
        record = manifest["binaries"][index]
        (self.output / record["archive"]["name"]).write_bytes(data)
        record["archive"].update(size=len(data), sha256=hashlib.sha256(data).hexdigest())
        path.write_text(json.dumps(manifest), encoding="utf-8")
        self.refresh_checksums()
        return record

    def test_archive_rejects_unsafe_members_extensions_and_noncanonical_headers(self):
        self.make_package()
        original = gzip.decompress((self.output / ASSETS[0]).read_bytes())
        mutations = [
            (0, b"../panel"), (0, b"/panel"), (0, b"./panel"), (0, b"dir/panel"),
            (0, b"C:\\panel"), (0, b"panel\\evil"), (0, b"other"),
            (345, b"prefix"), (157, b"linked"), (100, b"0000644\0"),
            (257, b"ustar "), (108, b"0000001\0"),
        ]
        mutations += [(156, kind) for kind in (b"\0", b"1", b"2", b"3", b"4", b"5", b"6", b"7", b"x", b"g", b"L", b"K", b"S")]
        for offset, replacement in mutations:
            header = bytearray(original[:512])
            if offset == 0:
                header[:100] = b"\0" * 100
            header[offset:offset + len(replacement)] = replacement
            header[148:156] = b" " * 8
            header[148:156] = f"{sum(header):06o}\0 ".encode("ascii")
            with self.subTest(offset=offset, replacement=replacement):
                self.replace_archive(gzip.compress(bytes(header) + original[512:], mtime=0))
                with self.assertRaises(release.ReleaseError):
                    self.verify()

    def test_archive_rejects_extra_member_padding_and_trailing_data(self):
        self.make_package()
        original = gzip.decompress((self.output / ASSETS[0]).read_bytes())
        extra_member = original[:1024] + original
        nonzero_padding = original[:700] + b"x" + original[701:]
        cases = (extra_member, nonzero_padding, original + b"x" * 512,
                 original[:1024], original[:1536], original[:-1], original[:600])
        for index, data in enumerate(cases):
            with self.subTest(case=index):
                self.replace_archive(gzip.compress(data, mtime=0))
                with self.assertRaises(release.ReleaseError):
                    self.verify()

    def test_archive_rejects_gzip_integrity_truncation_concatenation_and_bomb(self):
        self.make_package()
        original = (self.output / ASSETS[0]).read_bytes()
        corrupted = bytearray(original)
        corrupted[-8] ^= 1
        cases = (original[:-1], original[:20], bytes(corrupted), original + original,
                 original + b"\0", original + b"trailing", b"not gzip")
        for index, data in enumerate(cases):
            with self.subTest(case=index):
                self.replace_archive(data)
                with self.assertRaises(release.ReleaseError):
                    self.verify()
        self.replace_archive(gzip.compress(b"\0" * (128 * 1024), mtime=0))
        with mock.patch.object(release, "MAX_TAR_SIZE", 4096):
            with self.assertRaisesRegex(release.ReleaseError, "uncompressed tar"):
                self.verify()

    def test_archive_limit_and_hash_are_checked_before_decompression(self):
        manifest = self.make_package()
        record = manifest["binaries"][0]
        archive = self.output / record["archive"]["name"]
        archive.write_bytes(archive.read_bytes() + b"tamper")
        self.refresh_checksums()
        with mock.patch.object(release, "_gunzip_bounded") as decoder:
            with self.assertRaises(release.ReleaseError):
                self.verify()
            decoder.assert_not_called()
        with mock.patch.object(release, "MAX_ARCHIVE_SIZE", 1):
            with self.assertRaises(release.ReleaseError):
                self.verify()

    def test_archive_inner_hash_size_arch_and_nested_fields(self):
        manifest = self.make_package()
        original = manifest["binaries"][0]
        mutations = (
            {"sha256": "b" * 64}, {"size": original["size"] + 1}, {"size": True},
            {"size": 0}, {"size": release.MAX_BINARY_SIZE + 1}, {"name": "panel-linux-amd64"},
            {"arch": "arm64"}, {"extra": 1},
            {"archive": dict(original["archive"], size=True)},
            {"archive": dict(original["archive"], sha256="A" * 64)},
            {"archive": dict(original["archive"], name="../panel.tar.gz")},
            {"archive": dict(original["archive"], extra=1)}, {"archive": None},
        )
        for mutation in mutations:
            modified = dict(manifest, binaries=[dict(original, **mutation), *manifest["binaries"][1:]])
            (self.output / "release.json").write_text(json.dumps(modified), encoding="utf-8")
            self.refresh_checksums()
            with self.subTest(mutation=mutation):
                with self.assertRaises(release.ReleaseError):
                    self.verify()
        (self.output / "release.json").write_text(json.dumps(manifest), encoding="utf-8")
        tar = gzip.decompress((self.output / ASSETS[0]).read_bytes())
        wrong_elf = elf_fixture("arm64")
        self.replace_archive(gzip.compress(tar[:512] + wrong_elf + tar[512 + len(wrong_elf):], mtime=0))
        changed = json.loads((self.output / "release.json").read_bytes())
        changed["binaries"][0]["sha256"] = hashlib.sha256(wrong_elf).hexdigest()
        (self.output / "release.json").write_text(json.dumps(changed), encoding="utf-8")
        self.refresh_checksums()
        with self.assertRaisesRegex(release.ReleaseError, "Linux amd64 ELF"):
            self.verify()

    def test_extraction_is_exclusive_fixed_name_and_never_executes(self):
        manifest = self.make_package()
        record = manifest["binaries"][0]
        archive = self.output / record["archive"]["name"]
        destination = self.root / "panel"
        with mock.patch.object(release.subprocess, "run") as runner:
            self.assertEqual(release.extract_archive(archive, record, destination), destination)
            self.assertEqual(destination.read_bytes(), elf_fixture("amd64"))
            self.verify()
            runner.assert_not_called()
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, record, destination)
        self.assertEqual(destination.read_bytes(), elf_fixture("amd64"))
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, record, self.root / "wrong-name")
        destination.unlink()
        bad = dict(record, sha256="b" * 64)
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, bad, destination)
        self.assertFalse(destination.exists())

    def test_copy_is_bounded_when_source_grows_or_shrinks(self):
        destination = io.BytesIO()
        with self.assertRaises(release.ReleaseError):
            release._copy_exact(io.BytesIO(b"12345"), destination, 4, eof=True)
        self.assertEqual(destination.getvalue(), b"1234")
        with self.assertRaises(release.ReleaseError):
            release._copy_exact(io.BytesIO(b"123"), io.BytesIO(), 4, eof=True)
        manifest = self.make_package()
        record = dict(manifest["binaries"][0])
        record.pop("archive")
        binary = self.root / "growing-source"
        binary.write_bytes(elf_fixture("amd64") + b"changed")
        out = self.root / "copy-bound"
        out.mkdir()
        with self.assertRaises(release.ReleaseError):
            release._archive_binary(binary, record, out)

    def test_windows_missing_scripts_fail_before_creating_output(self):
        for name in ("install.ps1", "update.ps1"):
            script = self.project / "scripts" / name
            script.unlink()
            with mock.patch.object(release.subprocess, "run") as runner:
                with self.assertRaises(release.ReleaseError):
                    self.build()
                runner.assert_not_called()
            self.assertFalse(self.output.exists())
            script.write_bytes(self.scripts[name])

    def test_windows_pe_validation_rejects_wrong_machine_dll_and_truncation(self):
        bad_offset = bytearray(pe_fixture())
        struct.pack_into("<I", bad_offset, 60, 0xffffffff)
        bad_section = bytearray(pe_fixture())
        struct.pack_into("<I", bad_section, 64 + 24 + 112 + 20, 0xffffffff)
        cases = (b"not a PE", elf_fixture("amd64"), pe_fixture(machine=0x14c), pe_fixture(machine=0xaa64),
                 pe_fixture(magic=0x10b), pe_fixture(flags=0x2022), pe_fixture(flags=0),
                 pe_fixture()[:63], pe_fixture()[:84], pe_fixture()[:200], bytes(bad_offset), bytes(bad_section))
        path = self.root / "inert.exe"
        for index, data in enumerate(cases):
            path.write_bytes(data)
            with self.subTest(case=index):
                with self.assertRaises(release.ReleaseError):
                    release._check_pe(path, len(data))
        path.write_bytes(pe_fixture())
        release._check_pe(path, path.stat().st_size)

    def test_windows_zip_accepts_store_deflate_utf8_and_safe_dos_creator(self):
        self.make_package()
        for method in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
            for utf8 in (False, True):
                raw = bytearray(zip_fixture(method=method))
                central = raw.index(b"PK\x01\x02")
                if utf8:
                    struct.pack_into("<H", raw, 6, 0x800)
                    struct.pack_into("<H", raw, central + 8, 0x800)
                self.replace_archive(bytes(raw), index=2)
                with self.subTest(method=method, utf8=utf8):
                    self.verify()
        raw = bytearray(zip_fixture())
        central = raw.index(b"PK\x01\x02")
        struct.pack_into("<H", raw, central + 4, 20)
        struct.pack_into("<I", raw, central + 38, 0x20)
        self.replace_archive(bytes(raw), index=2)
        self.verify()

    def test_windows_zip_rejects_paths_links_types_extra_comments_and_zip64(self):
        self.make_package()
        mutations = [
            {"name": "../panel.exe"}, {"name": "/panel.exe"}, {"name": "./panel.exe"},
            {"name": "C:\\panel.exe"}, {"name": "dir/panel.exe"}, {"name": "panel.exe:stream"},
            {"name": "panel.exe/"}, {"name": "PANEL.EXE"}, {"name": "panel"},
            {"mode": stat.S_IFLNK | 0o755}, {"mode": stat.S_IFDIR | 0o755},
            {"mode": stat.S_IFIFO | 0o755}, {"mode": stat.S_IFCHR | 0o755}, {"mode": 0o755},
            {"extra": b"\xfe\xca\0\0"}, {"comment": b"not allowed"},
            {"archive_comment": b"not allowed"}, {"extra_member": True}, {"force_zip64": True},
            {"method": zipfile.ZIP_BZIP2},
        ]
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                self.replace_archive(zip_fixture(**mutation), index=2)
                with self.assertRaises(release.ReleaseError):
                    self.verify()

    def test_windows_zip_rejects_header_disagreement_encryption_and_multivolume(self):
        self.make_package()
        original = zip_fixture()
        central = original.index(b"PK\x01\x02")
        end = len(original) - 22
        cases = [(6, "H", 1), (central + 8, "H", 1), (6, "H", 8),
                 (central + 8, "H", 8), (8, "H", 99), (central + 10, "H", 99),
                 (4, "H", 45), (central + 6, "H", 45),
                 (central + 42, "I", 1), (central + 38, "I", 0x10),
                 (central + 38, "I", (stat.S_IFREG | 0o755) << 16 | 0x400),
                 (central + 34, "H", 1), (end + 4, "H", 1), (end + 6, "H", 1),
                 (end + 8, "H", 2), (end + 10, "H", 2),
                 (end + 12, "I", 0xffffffff), (end + 16, "I", 0xffffffff)]
        for offset, fmt, value in cases:
            raw = bytearray(original)
            struct.pack_into("<" + fmt, raw, offset, value)
            with self.subTest(offset=offset, value=value):
                self.replace_archive(bytes(raw), index=2)
                with self.assertRaises(release.ReleaseError):
                    self.verify()
        for raw in (b"prefix" + original, original + b"\0", original + original, original[:-1], original[:20]):
            self.replace_archive(raw, index=2)
            with self.assertRaises(release.ReleaseError):
                self.verify()

    def test_windows_zip_crc_hash_arch_truncation_and_bomb(self):
        self.make_package()
        original = zip_fixture()
        central = original.index(b"PK\x01\x02")
        corrupted = bytearray(original)
        crc = struct.unpack_from("<I", corrupted, 14)[0] ^ 1
        struct.pack_into("<I", corrupted, 14, crc)
        struct.pack_into("<I", corrupted, central + 16, crc)
        self.replace_archive(bytes(corrupted), index=2)
        with self.assertRaisesRegex(release.ReleaseError, "CRC"):
            self.verify()
        self.replace_archive(zip_fixture(data=pe_fixture(machine=0xaa64)), index=2)
        metadata = json.loads((self.output / "release.json").read_bytes())
        metadata["binaries"][2]["sha256"] = hashlib.sha256(pe_fixture(machine=0xaa64)).hexdigest()
        (self.output / "release.json").write_text(json.dumps(metadata), encoding="utf-8")
        self.refresh_checksums()
        with self.assertRaisesRegex(release.ReleaseError, "Windows amd64"):
            self.verify()
        self.replace_archive(original, index=2)
        with self.assertRaisesRegex(release.ReleaseError, "SHA-256"):
            self.verify()
        # Keep local/central metadata mutually consistent while a deflate stream
        # produces more bytes: decoder must reject at the declared bound, not CRC.
        bomb = bytearray(zip_fixture(data=b"\0" * (256 * 1024)))
        central = bomb.index(b"PK\x01\x02")
        struct.pack_into("<I", bomb, 22, len(pe_fixture()))
        struct.pack_into("<I", bomb, central + 24, len(pe_fixture()))
        self.replace_archive(bytes(bomb), index=2)
        with self.assertRaisesRegex(release.ReleaseError, "expansion exceeds"):
            self.verify()
        # Remove a byte from compressed data but repair container sizes/offsets.
        central = original.index(b"PK\x01\x02")
        truncated = bytearray(original[:central - 1] + original[central:])
        compressed = struct.unpack_from("<I", original, 18)[0] - 1
        struct.pack_into("<I", truncated, 18, compressed)
        struct.pack_into("<I", truncated, central - 1 + 20, compressed)
        struct.pack_into("<I", truncated, len(truncated) - 6, central - 1)
        self.replace_archive(bytes(truncated), index=2)
        with self.assertRaises(release.ReleaseError):
            self.verify()

    def test_windows_zip_extraction_is_exclusive_bounded_and_never_executes(self):
        manifest = self.make_package()
        record = manifest["binaries"][2]
        archive = self.output / record["archive"]["name"]
        destination = self.root / "panel.exe"
        with mock.patch.object(release.subprocess, "run") as runner:
            release.extract_archive(archive, record, destination)
            self.assertEqual(destination.read_bytes(), pe_fixture())
            runner.assert_not_called()
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, record, destination)
        self.assertEqual(destination.read_bytes(), pe_fixture())
        destination.unlink()
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, dict(record, sha256="b" * 64), destination)
        self.assertFalse(destination.exists())
        with self.assertRaises(release.ReleaseError):
            release.extract_archive(archive, record, self.root / "panel")
        archive.write_bytes(archive.read_bytes() + b"tamper")
        with mock.patch.object(release, "_unzip_bounded") as decoder:
            with self.assertRaises(release.ReleaseError):
                release.extract_archive(archive, record, destination)
            decoder.assert_not_called()
        self.assertFalse(destination.exists())
        # Building either compressed format must stop before crossing its bound.
        for target_os, arch, data in (("linux", "amd64", elf_fixture("amd64")), ("windows", "amd64", pe_fixture())):
            binary = self.root / ("source-" + target_os)
            binary.write_bytes(data)
            record = release._binary_record(binary, target_os, arch)
            output = self.root / ("limited-" + target_os)
            output.mkdir()
            with mock.patch.object(release, "MAX_ARCHIVE_SIZE", 50):
                with self.assertRaises(release.ReleaseError):
                    release._archive_binary(binary, record, output)
            self.assertLessEqual(next(output.iterdir()).stat().st_size, 50)

    def test_format2_and_missing_or_duplicate_targets_are_not_publishable(self):
        manifest = self.make_package()
        mutations = ({"format": 2}, {"binaries": manifest["binaries"][:2]},
                     {"binaries": [manifest["binaries"][0]] * 3},
                     {"binaries": list(reversed(manifest["binaries"]))})
        for mutation in mutations:
            (self.output / "release.json").write_text(json.dumps(dict(manifest, **mutation)), encoding="utf-8")
            self.refresh_checksums()
            with self.assertRaises(release.ReleaseError):
                self.verify()

    def test_cli_verify_format3_does_not_require_go_or_execute_binaries(self):
        self.make_package()
        result = subprocess.run([
            sys.executable, "-I", str(TOOL), "--verify-only", "--version", TAG, "--repo", REPOSITORY,
            "--commit", COMMIT, "--build-time", BUILD_TIME, "--output", str(self.output), "--go", "missing-go",
        ], cwd=self.root, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_symlink_output_parent_or_script_is_rejected(self):
        target = self.root / "real-parent"
        target.mkdir()
        link = self.root / "linked-parent"
        try:
            link.symlink_to(target, target_is_directory=True)
        except OSError as error:
            self.skipTest(f"symlink creation unavailable: {error}")
        with self.assertRaises(release.ReleaseError):
            self.build(output=link / "release")
        self.assertEqual(list(target.iterdir()), [])
        script = self.project / "scripts" / "install.sh"
        script.unlink()
        script.symlink_to(self.project / "scripts" / "update.sh")
        with self.assertRaises(release.ReleaseError):
            self.build()
        self.assertFalse(self.output.exists())

    def test_cli_requires_commit_and_reports_validation_without_traceback(self):
        arguments = [
            sys.executable, "-I", str(TOOL), "--version", TAG, "--repo", REPOSITORY,
            "--output", str(self.output),
        ]
        for suffix in ([], ["--commit", "unknown"]):
            result = subprocess.run(arguments + suffix, cwd=self.root, capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 2)
            self.assertNotIn("Traceback", result.stderr)
            self.assertFalse(self.output.exists())


if __name__ == "__main__":
    unittest.main()
