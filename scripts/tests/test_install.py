"""Isolated installer tests: python3 -I scripts/tests/test_install.py.

The production heredoc is compiled in memory without calling main(). All paths,
service/account operations, downloads, and binary execution use test adapters.
No subprocess, sudo, systemctl, useradd, Docker, or real network is invoked.
"""
import contextlib
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import sys
import tarfile
import tempfile
import types
import unittest
import zipfile
from unittest import mock

SCRIPTS = Path(__file__).resolve().parents[1]
SOURCE = (SCRIPTS / "install.sh").read_bytes()
BODY = SOURCE.decode("utf-8").split("<<'PANEL_PYTHON'\n", 1)[1].rsplit("\nPANEL_PYTHON\n", 1)[0]
panel = types.ModuleType("panel_installer_test")
exec(compile(BODY, str(SCRIPTS / "install.sh") + "::python", "exec"), panel.__dict__)
REPO = "example-owner/example-panel"


def encoded(value):
    return json.dumps(value, separators=(",", ":")).encode("utf-8")


def binary(version="0.3.0", arch="amd64", repo=REPO):
    header = bytearray(64)
    header[:6] = b"\x7fELF\x02\x01"
    header[18:20] = {"amd64": 62, "arm64": 183}[arch].to_bytes(2, "little")
    return bytes(header) + encoded({"version": version, "commit": "a" * 40, "buildTime": "2026-11-06T00:00:00Z",
                                   "repository": repo, "dataSchema": 1, "os": "linux", "arch": arch})


def binary_details(path):
    return json.loads(Path(path).read_bytes()[64:])


def archive(raw):
    output = io.BytesIO()
    with gzip.GzipFile(fileobj=output, filename="", mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as tar:
            member = tarfile.TarInfo("panel")
            member.mode, member.size, member.mtime = 0o755, len(raw), 0
            tar.addfile(member, io.BytesIO(raw))
    return output.getvalue()


def refresh_release(data, metadata, repo=REPO):
    data["release.json"] = encoded(metadata)
    data["SHA256SUMS"] = b"".join(hashlib.sha256(raw).hexdigest().encode() + b"  " + name.encode() + b"\n"
                                     for name, raw in sorted(data.items()) if name != "SHA256SUMS")
    tag = metadata["tag"]
    api = {"draft": False, "prerelease": False, "tag_name": tag,
           "html_url": "https://github.com/" + repo + "/releases/tag/" + tag,
           "assets": [{"name": name, "size": len(raw), "state": "uploaded", "browser_download_url": panel.canonical(repo, tag, name)}
                      for name, raw in sorted(data.items())]}
    return data, api


def release(version="0.3.0", repo=REPO):
    # Synthetic identities/binaries only: this is not a real public release.
    tag = "v" + version
    data = {"install.sh": SOURCE, "update.sh": panel.WRAPPER, "install.ps1": b"# synthetic installer\n", "update.ps1": b"# synthetic updater\n"}
    metadata = {"format": 3, "version": version, "tag": tag, "repository": repo, "dataSchema": 1,
                "commit": "a" * 40, "buildTime": "2026-11-06T00:00:00Z", "binaries": []}
    for arch in ("amd64", "arm64"):
        name = "panel-linux-" + arch + ".tar.gz"
        raw = binary(version, arch, repo)
        data[name] = archive(raw)
        metadata["binaries"].append({"os": "linux", "arch": arch, "name": "panel", "size": len(raw),
                                     "sha256": hashlib.sha256(raw).hexdigest(),
                                     "archive": {"name": name, "size": len(data[name]), "sha256": hashlib.sha256(data[name]).hexdigest()}})
    windows = b"MZ synthetic Windows fixture, never executed"
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED) as packed:
        packed.writestr("panel.exe", windows)
    data["panel-windows-amd64.zip"] = output.getvalue()
    metadata["binaries"].append({"os": "windows", "arch": "amd64", "name": "panel.exe", "size": len(windows),
                                 "sha256": hashlib.sha256(windows).hexdigest(),
                                 "archive": {"name": "panel-windows-amd64.zip", "size": len(output.getvalue()),
                                             "sha256": hashlib.sha256(output.getvalue()).hexdigest()}})
    return refresh_release(data, metadata, repo)


class FakeFiles(panel.SafeFiles):
    """Real bytes/atomic rename in a temporary root, simulated POSIX owners/modes."""
    def __init__(self, root, events):
        self.root, self.events = Path(root), events
        self.attributes = {}

    def within(self, path):
        try:
            Path(path).relative_to(self.root)
        except ValueError as exc:
            raise AssertionError("Test attempted to access outside its temporary root: " + str(path)) from exc

    def info(self, path):
        path = Path(path)
        self.within(path)
        entry = os.lstat(path)
        default_mode = 0o755 if stat.S_ISDIR(entry.st_mode) else 0o600
        values = {"uid": 0, "gid": 0, "mode": default_mode, "kind": stat.S_IFMT(entry.st_mode), "links": entry.st_nlink}
        values.update(self.attributes.get(path, {}))
        return types.SimpleNamespace(st_uid=values["uid"], st_gid=values["gid"], st_mode=values["kind"] | values["mode"],
                                     st_dev=entry.st_dev, st_ino=entry.st_ino, st_size=entry.st_size, st_nlink=values["links"])

    def owner_mode(self, path, uid, gid, mode):
        self.within(path)
        self.attributes[Path(path)] = {"uid": uid, "gid": gid, "mode": mode}

    def parents(self, path):
        self.within(path)
        for parent in reversed(Path(path).parents):
            if parent != self.root and self.root not in parent.parents:
                continue
            entry = self.info(parent)
            panel.require(stat.S_ISDIR(entry.st_mode) and entry.st_uid == 0 and not entry.st_mode & 0o022,
                          "Unsafe fake parent: " + str(parent))

    def sync_dir(self, path):
        self.within(path)
        self.events.append(("sync-dir", str(path)))

    def replace(self, source, target):
        self.within(source)
        self.within(target)
        super().replace(source, target)
        self.attributes[Path(target)] = self.attributes.pop(Path(source), {})
        self.events.append(("replace", Path(target).name))


class FakeRuntime:
    def __init__(self, paths, events):
        self.paths, self.events = paths, events
        self.identity = None
        self.active = False
        self.fail_health = set()
        self.fail_start = set()
        self.fail_info = False
        self.fail_stop_once = False
        self.interrupt_start_once = False
        self.locked = False
        self.busy = False

    @contextlib.contextmanager
    def lock(self, _paths, _files):
        panel.require(not self.busy, "Another update holds the management lock.")
        assert not self.locked
        self.locked = True
        self.events.append(("lock", "acquired"))
        try:
            yield
        finally:
            self.events.append(("lock", "released"))
            self.locked = False

    def account(self, data, create=False):
        assert data == self.paths.data
        if create:
            assert self.locked and self.identity is None
            self.identity = (991, 991)
            self.events.append(("account", "created"))
        return self.identity

    def inspect_unit(self, expected):
        assert expected is None or expected == self.paths.unit
        self.events.append(("inspect-unit", expected is not None))

    def binary_info(self, path):
        assert self.locked
        self.events.append(("build-info", "staged-panel" if Path(path).parent != self.paths.binary.parent else Path(path).name))
        panel.require(not self.fail_info, "Fake binary build-info failure.")
        return encoded(binary_details(path))

    def systemctl(self, verb, *options, **_kwargs):
        assert self.locked and verb in {"stop", "start", "enable", "disable", "daemon-reload", "is-active"}
        assert not options or options == ("--quiet",)
        self.events.append(("systemctl", verb))
        if verb == "stop":
            self.active = False
            if self.fail_stop_once:
                self.fail_stop_once = False
                raise panel.Failure("Injected stop failure after stopping.")
        elif verb == "start":
            if self.interrupt_start_once:
                self.interrupt_start_once = False
                raise KeyboardInterrupt()
            panel.require(binary_details(self.paths.binary)["version"] not in self.fail_start, "Injected start failure.")
            self.active = True
        elif verb == "is-active":
            return (0 if self.active else 3), b""
        return 0, b""

    def healthy(self, config):
        self.events.append(("healthy", config["version"]))
        panel.require(self.active and binary_details(self.paths.binary)["version"] == config["version"], "Wrong running version.")
        panel.require(config["version"] not in self.fail_health, "Injected health failure.")

    def confirm(self, message, unattended):
        assert unattended is True
        self.events.append(("confirm", message))

    @contextlib.contextmanager
    def rollback_signals(self):
        yield


class FakeDownloads:
    def __init__(self, runtime, events):
        self.runtime, self.events = runtime, events
        self.assets, self.api = release()

    def use(self, version):
        self.assets, self.api = release(version)

    def get(self, url, limit, timeout, destination=None, api=False):
        assert self.runtime.locked
        assert 0 < timeout <= (15 if api else 120)
        self.events.append(("download", url))
        raw = encoded(self.api) if api else self.assets[url.rsplit("/", 1)[1]]
        panel.require(len(raw) <= limit, "Fake download size limit.")
        if destination is None:
            return raw
        with open(destination, "xb") as output:
            output.write(raw)
        return len(raw), hashlib.sha256(raw).hexdigest()


class InstallationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="panel-installer-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.paths = types.SimpleNamespace(**{name: self.root.joinpath(*getattr(panel.Paths, name).parts[1:])
                                              for name in ("binary", "previous", "code_dir", "installer", "manager_previous", "updater", "config_dir", "config", "transaction", "migration", "unit", "data", "lock_dir", "lock")})
        for path in (self.paths.binary.parent, self.paths.code_dir.parent, self.paths.unit.parent, self.paths.config_dir.parent,
                     self.paths.data.parent, self.paths.lock_dir.parent):
            path.mkdir(parents=True, exist_ok=True)
        self.events = []
        self.files = FakeFiles(self.root, self.events)
        self.runtime = FakeRuntime(self.paths, self.events)
        self.downloads = FakeDownloads(self.runtime, self.events)
        self.manager = panel.Manager(self.paths, self.files, self.runtime, self.downloads)

    def execute(self, update=False, version=None, external_origin=None):
        args = ["--update"] if update else ["--repo", REPO]
        if version:
            args.extend(["--version", version])
        if external_origin:
            args.extend(["--origin", external_origin])
        args.append("--yes")
        with contextlib.redirect_stdout(io.StringIO()):
            self.manager.execute(panel.arguments(args), SOURCE, "amd64")

    def install(self, external_origin=None):
        self.execute(external_origin=external_origin)
        (self.paths.data / "accounts.json").write_bytes(b"private account bytes")
        (self.paths.data / ".panel.lock").write_bytes(b"retained lock inode")
        self.events.clear()

    def assert_data(self):
        self.assertEqual((self.paths.data / "accounts.json").read_bytes(), b"private account bytes")
        self.assertEqual((self.paths.data / ".panel.lock").read_bytes(), b"retained lock inode")

    def assert_no_stop(self):
        self.assertNotIn(("systemctl", "stop"), self.events)

    def test_install_creates_fixed_private_layout_after_verification(self):
        self.execute(external_origin="https://PANEL.example.com:443/")
        config = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(config["origin"], "https://panel.example.com")
        self.assertEqual(config["healthHost"], "panel.example.com")
        self.assertEqual(self.paths.updater.read_bytes(), (SCRIPTS / "update.sh").read_bytes())
        self.assertEqual(self.paths.installer.read_bytes(), SOURCE)
        self.assertEqual(self.files.info(self.paths.data).st_uid, 991)
        self.assertEqual(stat.S_IMODE(self.files.info(self.paths.data).st_mode), 0o700)
        for path, mode in ((self.paths.binary, 0o755), (self.paths.installer, 0o755), (self.paths.config, 0o644), (self.paths.unit, 0o644)):
            self.files.check(path, mode=mode)
        verification = self.events.index(("build-info", "staged-panel"))
        self.assertLess(verification, self.events.index(("account", "created")))
        self.assertLess(self.events.index(("healthy", "0.3.0")), self.events.index(("replace", "install.json")))
        self.assert_no_stop()
        self.assertFalse(any(url.endswith("/install.sh") or url.endswith("/update.sh") for kind, url in self.events if kind == "download"))

    def test_repeat_install_preserves_data_and_configuration(self):
        self.install(external_origin="https://panel.example.com")
        config = self.paths.config.read_bytes()
        self.execute(version="v0.3.1")
        self.assertEqual(self.paths.config.read_bytes(), config)
        self.assert_data()
        self.assertFalse(any(kind in {"download", "account", "systemctl", "replace"} for kind, _ in self.events))

    def test_successful_update_keeps_previous_and_checks_health_before_metadata(self):
        self.install()
        old = self.paths.binary.read_bytes()
        self.downloads.use("0.3.1")
        self.execute(update=True)
        self.assertEqual(binary_details(self.paths.binary)["version"], "0.3.1")
        self.assertEqual(self.paths.previous.read_bytes(), old)
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes())["version"], "0.3.1")
        self.assert_data()
        self.assertLess(self.events.index(("build-info", "staged-panel")), self.events.index(("systemctl", "stop")))
        self.assertLess(self.events.index(("healthy", "0.3.1")), self.events.index(("replace", "install.json")))
        self.assertFalse(list(self.paths.binary.parent.glob(".server-panel-stage-*")))
        self.assertFalse(self.paths.transaction.exists())
        marker_writes = [index for index, item in enumerate(self.events) if item == ("replace", "update-transaction.json")]
        self.assertEqual(len(marker_writes), 2)
        self.assertLess(marker_writes[0], self.events.index(("replace", "panel.previous")))
        self.assertLess(self.events.index(("replace", "panel.previous")), marker_writes[1])
        self.assertLess(marker_writes[1], self.events.index(("systemctl", "stop")))
        self.assertEqual(self.events[-2], ("sync-dir", str(self.paths.config_dir)))

    def test_same_version_and_downgrade_never_stop(self):
        self.install()
        self.execute(update=True)
        self.assert_no_stop()
        self.events.clear()
        self.downloads.use("0.2.9")
        with self.assertRaisesRegex(panel.Failure, "Downgrades"):
            self.execute(update=True, version="v0.2.9")
        self.assert_no_stop()
        self.assert_data()

    def test_download_tampering_fails_before_execution_or_stop(self):
        self.install()
        self.downloads.use("0.3.1")
        raw = self.downloads.assets["panel-linux-amd64.tar.gz"]
        self.downloads.assets["panel-linux-amd64.tar.gz"] = raw[:-1] + b"x"
        with self.assertRaisesRegex(panel.Failure, "SHA-256"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertNotIn(("build-info", "staged-panel"), self.events)
        self.assert_data()

    def test_build_info_mismatch_fails_before_stop(self):
        self.install()
        self.downloads.use("0.3.1")
        original = self.runtime.binary_info

        def mismatch(path):
            data = json.loads(original(path))
            if Path(path) != self.paths.binary:
                data["repository"] = "other/repository"
            return encoded(data)

        self.runtime.binary_info = mismatch
        with self.assertRaisesRegex(panel.Failure, "build-info"):
            self.execute(update=True)
        self.assert_no_stop()

    def test_corrupt_manifest_never_stops(self):
        self.install()
        self.downloads.use("0.3.1")
        self.downloads.assets["release.json"] = self.downloads.assets["release.json"].replace(b'"format":3', b'"format":1')
        with self.assertRaisesRegex(panel.Failure, "Manifest SHA-256"):
            self.execute(update=True)
        self.assert_no_stop()

    def replace_release_payload(self, raw=None, metadata_change=None, packed=None):
        metadata = json.loads(self.downloads.assets["release.json"])
        selected = metadata["binaries"][0]
        name = selected["archive"]["name"]
        if raw is not None:
            selected.update(size=len(raw), sha256=hashlib.sha256(raw).hexdigest())
            packed = archive(raw)
        if packed is not None:
            self.downloads.assets[name] = packed
            selected["archive"].update(size=len(packed), sha256=hashlib.sha256(packed).hexdigest())
        if metadata_change is not None:
            metadata_change(selected)
        self.downloads.assets, self.downloads.api = refresh_release(self.downloads.assets, metadata)

    def test_installed_identity_is_inner_elf_not_compressed_archive(self):
        self.execute()
        config = panel.installation_config(self.paths.config.read_bytes())
        metadata = json.loads(self.downloads.assets["release.json"])
        selected = metadata["binaries"][0]
        self.assertEqual(self.paths.binary.read_bytes(), binary())
        self.assertEqual(config["binarySHA256"], selected["sha256"])
        self.assertNotEqual(config["binarySHA256"], selected["archive"]["sha256"])
        self.assertEqual(self.paths.binary.stat().st_size, selected["size"])
        self.assertEqual(config["format"], 1)

    def test_inner_hash_mismatch_never_executes_candidate_or_stops(self):
        self.install()
        self.downloads.use("0.3.1")
        self.replace_release_payload(metadata_change=lambda item: item.update(sha256="f" * 64))
        with self.assertRaisesRegex(panel.Failure, "Inner binary SHA-256"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertNotIn(("build-info", "staged-panel"), self.events)
        self.assert_data()

    def test_inner_size_mismatch_never_executes_candidate_or_stops(self):
        self.install()
        self.downloads.use("0.3.1")
        self.replace_release_payload(metadata_change=lambda item: item.update(size=item["size"] + 1))
        with self.assertRaisesRegex(panel.Failure, "tar/manifest size"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertNotIn(("build-info", "staged-panel"), self.events)
        self.assert_data()

    def test_archive_api_size_must_match_both_architectures(self):
        self.install()
        for arch in ("amd64", "arm64"):
            with self.subTest(arch=arch):
                self.downloads.use("0.3.1")
                self.events.clear()
                next(item for item in self.downloads.api["assets"] if item["name"] == "panel-linux-" + arch + ".tar.gz")["size"] += 1
                with self.assertRaisesRegex(panel.Failure, "API/manifest size"):
                    self.execute(update=True)
                self.assert_no_stop()
                self.assertNotIn(("build-info", "staged-panel"), self.events)

    def test_archive_disk_identity_is_rechecked_before_decompression(self):
        self.install()
        self.downloads.use("0.3.1")
        original = self.downloads.get

        def changed_on_disk(*args, **kwargs):
            result = original(*args, **kwargs)
            path = kwargs.get("destination")
            if path is not None:
                raw = path.read_bytes()
                path.write_bytes(raw[:-1] + bytes([raw[-1] ^ 1]))
            return result

        self.downloads.get = changed_on_disk
        with mock.patch.object(panel, "extract_panel", side_effect=AssertionError("Unverified archive was expanded")):
            with self.assertRaisesRegex(panel.Failure, "Staged archive SHA-256"):
                self.execute(update=True)
        self.assert_no_stop()

    def test_verified_archive_still_requires_linux_elf_and_matching_arch(self):
        self.install()
        for raw in (b"not an ELF file", binary("0.3.1", "arm64"), b"\x7fELF\x01\x01" + binary("0.3.1")[6:]):
            with self.subTest(raw=raw[:20]):
                self.downloads.use("0.3.1")
                self.replace_release_payload(raw=raw)
                self.events.clear()
                with self.assertRaisesRegex(panel.Failure, "ELF architecture"):
                    self.execute(update=True)
                self.assert_no_stop()
                self.assertNotIn(("build-info", "staged-panel"), self.events)
        self.assert_data()

    def test_verified_archive_gzip_failure_leaves_service_and_data_intact(self):
        self.install()
        self.downloads.use("0.3.1")
        packed = self.downloads.assets["panel-linux-amd64.tar.gz"]
        self.replace_release_payload(packed=packed[:-1])
        with self.assertRaisesRegex(panel.Failure, "Truncated gzip"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertNotIn(("build-info", "staged-panel"), self.events)
        self.assertFalse(list(self.paths.binary.parent.glob(".server-panel-stage-*")))
        self.assert_data()

    def assert_rollback(self, failure_setup):
        self.install()
        old_binary, old_config = self.paths.binary.read_bytes(), self.paths.config.read_bytes()
        self.downloads.use("0.3.1")
        failure_setup()
        with self.assertRaisesRegex(panel.Failure, "rollback succeeded"):
            self.execute(update=True)
        self.assertEqual(self.paths.binary.read_bytes(), old_binary)
        self.assertEqual(self.paths.previous.read_bytes(), old_binary)
        self.assertEqual(self.paths.config.read_bytes(), old_config)
        self.assertTrue(self.runtime.active)
        self.assertIn(("healthy", "0.3.0"), self.events)
        self.assertFalse(self.paths.transaction.exists())
        self.assert_data()

    def test_start_failure_restores_previous(self):
        self.assert_rollback(lambda: self.runtime.fail_start.add("0.3.1"))

    def test_health_failure_restores_previous(self):
        self.assert_rollback(lambda: self.runtime.fail_health.add("0.3.1"))

    def test_stop_failure_still_attempts_restore(self):
        self.assert_rollback(lambda: setattr(self.runtime, "fail_stop_once", True))

    def test_interrupt_after_replacement_restores_previous(self):
        self.assert_rollback(lambda: setattr(self.runtime, "interrupt_start_once", True))

    def test_metadata_write_failure_restores_previous_metadata(self):
        original = self.files.atomic_bytes
        once = [True]

        def fail_once(path, raw, mode):
            if path == self.paths.config and b'"version": "0.3.1"' in raw and once[0]:
                once[0] = False
                raise OSError("Injected metadata write failure")
            return original(path, raw, mode)

        self.files.atomic_bytes = fail_once
        self.assert_rollback(lambda: None)

    def test_failed_rollback_is_reported_and_preserves_previous(self):
        self.install()
        old = self.paths.binary.read_bytes()
        self.downloads.use("0.3.1")
        self.runtime.fail_health = {"0.3.0", "0.3.1"}
        with self.assertRaisesRegex(panel.Failure, "rollback also failed"):
            self.execute(update=True)
        self.assertEqual(self.paths.previous.read_bytes(), old)
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes())["version"], "0.3.0")
        self.assertTrue(self.paths.transaction.exists())
        self.assert_data()
        self.runtime.fail_health.clear()
        self.events.clear()
        self.execute(update=True)
        self.assertFalse(self.paths.transaction.exists())
        self.assertEqual(self.paths.previous.read_bytes(), old)
        self.assertFalse(any(kind == "download" for kind, _ in self.events))

    def test_failed_initial_health_records_no_success_and_retains_data(self):
        self.runtime.fail_health.add("0.3.0")
        with self.assertRaisesRegex(panel.Failure, "Initial installation failed"):
            self.execute()
        self.assertFalse(self.paths.config.exists())
        self.assertTrue(self.paths.data.exists())
        self.assertFalse(self.runtime.active)
        with self.assertRaisesRegex(panel.Failure, "Unknown or partial installation"):
            self.execute()

    def test_unknown_existing_binary_is_not_adopted(self):
        self.paths.binary.write_bytes(b"unknown binary")
        with self.assertRaisesRegex(panel.Failure, "Unknown or partial"):
            self.execute()
        self.assertEqual(self.paths.binary.read_bytes(), b"unknown binary")
        self.assertFalse(any(kind == "download" for kind, _ in self.events))

    def test_unknown_account_and_modified_unit_are_not_adopted(self):
        self.runtime.identity = (123, 123)
        with self.assertRaisesRegex(panel.Failure, "unknown service account"):
            self.execute()
        self.runtime.identity = None
        self.install()
        self.paths.unit.write_bytes(b"ExecStart=/bin/false\n")
        self.downloads.use("0.3.1")
        with self.assertRaisesRegex(panel.Failure, "modified service unit"):
            self.execute(update=True)
        self.assert_no_stop()

    def test_binary_and_management_file_integrity_are_checked(self):
        self.install()
        for path, message in ((self.paths.binary, "binary SHA-256"), (self.paths.installer, "management script"),
                              (self.paths.updater, "wrapper")):
            with self.subTest(path=path):
                original = path.read_bytes()
                path.write_bytes(original + b" ")
                with self.assertRaisesRegex(panel.Failure, message):
                    self.execute(update=True)
                path.write_bytes(original)
                self.assert_no_stop()

    def test_unsafe_file_owner_symlink_hardlink_and_parent_are_rejected(self):
        self.install()
        for changes in ({"uid": 991}, {"mode": 0o777}, {"kind": stat.S_IFLNK}, {"links": 2}):
            with self.subTest(changes=changes):
                before = dict(self.files.attributes[self.paths.binary])
                self.files.attributes[self.paths.binary].update(changes)
                with self.assertRaises(panel.Failure):
                    self.execute(update=True)
                self.files.attributes[self.paths.binary] = before
        self.files.attributes[self.paths.binary.parent] = {"uid": 991}
        with self.assertRaisesRegex(panel.Failure, "parent"):
            self.execute(update=True)
        self.assert_no_stop()

    def test_busy_independent_lock_prevents_all_work(self):
        self.runtime.busy = True
        with self.assertRaisesRegex(panel.Failure, "lock"):
            self.execute()
        self.assertEqual(self.events, [])
        self.assertFalse(self.paths.binary.exists())

    def interrupted_fixture(self, phase="ready", switched=True, metadata_new=False, backup=True):
        """Direct post-crash fixture: no exception handler is allowed to perform cleanup."""
        self.install()
        old = panel.installation_config(self.paths.config.read_bytes())
        new = dict(old, version="0.3.1", binarySHA256=hashlib.sha256(binary("0.3.1")).hexdigest())
        if backup:
            self.files.atomic_bytes(self.paths.previous, self.paths.binary.read_bytes(), 0o755)
        if switched:
            self.files.atomic_bytes(self.paths.binary, binary("0.3.1"), 0o755)
        if metadata_new:
            self.files.atomic_bytes(self.paths.config, panel.config_bytes(new), 0o644)
        marker = {"format": 1, "phase": phase, "old": old, "new": new}
        self.files.atomic_bytes(self.paths.transaction, panel.config_bytes(marker), 0o600)
        self.runtime.active = False
        self.downloads.use("0.3.1")
        # A new manager models the next invocation; only disk/account/service fixtures survive.
        self.manager = panel.Manager(self.paths, self.files, self.runtime, self.downloads)
        self.events.clear()
        return marker

    def assert_recovered(self, marker):
        self.execute(update=True)
        self.assertEqual(self.paths.binary.read_bytes(), binary("0.3.0"))
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), marker["old"])
        self.assertFalse(self.paths.transaction.exists())
        self.assertTrue(self.runtime.active)
        self.assertIn(("healthy", "0.3.0"), self.events)
        self.assertFalse(any(kind in {"download", "confirm", "account"} for kind, _ in self.events))
        self.assertLess(self.events.index(("lock", "acquired")), self.events.index(("systemctl", "stop")))
        self.assertLess(self.events.index(("healthy", "0.3.0")), self.events.index(("replace", "install.json")))
        self.assert_data()

    def test_power_loss_after_binary_switch_recovers_before_normal_inspect(self):
        marker = self.interrupted_fixture()
        self.assert_recovered(marker)
        self.assertEqual(self.paths.previous.read_bytes(), binary("0.3.0"))
        self.assertLess(self.events.index(("build-info", "panel.previous")), self.events.index(("systemctl", "stop")))

    def test_power_loss_after_new_metadata_before_marker_clear_recovers_old(self):
        marker = self.interrupted_fixture(metadata_new=True)
        self.assert_recovered(marker)

    def test_prepared_phase_without_backup_recovers_verified_old_current(self):
        marker = self.interrupted_fixture(phase="prepared", switched=False, backup=False)
        self.assert_recovered(marker)
        self.assertFalse(self.paths.previous.exists())
        self.assertNotIn(("build-info", "panel.previous"), self.events)

    def test_ready_phase_before_switch_recovers_verified_old_current(self):
        marker = self.interrupted_fixture(switched=False)
        self.assert_recovered(marker)
        self.assertNotIn(("build-info", "panel.previous"), self.events)

    def test_prepared_phase_cannot_authorize_switched_binary(self):
        self.interrupted_fixture(phase="prepared")
        with self.assertRaisesRegex(panel.Failure, "backup phase"):
            self.execute(update=True)
        self.assertTrue(self.paths.transaction.exists())
        self.assert_no_stop()
        self.assertFalse(any(kind == "build-info" for kind, _ in self.events))

    def test_no_marker_does_not_adopt_replaced_binary(self):
        self.interrupted_fixture()
        self.paths.transaction.unlink()
        with self.assertRaisesRegex(panel.Failure, "Installed binary SHA-256 mismatch"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertEqual(binary_details(self.paths.binary)["version"], "0.3.1")

    def test_damaged_transaction_and_path_injection_fail_before_execution(self):
        marker = self.interrupted_fixture()
        good = panel.config_bytes(marker)
        cases = [b'{"format":', good.replace(b'"format": 1', b'"format": 1, "format": 1', 1)]
        for change in ({"format": True}, {"phase": "switched"}, {"path": "/tmp/untrusted"}, {"previous": "/tmp/untrusted"}):
            cases.append(panel.config_bytes(dict(marker, **change)))
        for field, value in (("binarySHA256", "f" * 64), ("repository", "other/repository"), ("version", "0.3.2"), ("dataSchema", 2)):
            bad = json.loads(good)
            bad["old"][field] = value
            cases.append(panel.config_bytes(bad))
        bad = json.loads(good)
        bad["new"]["binarySHA256"] = "f" * 64
        cases.append(panel.config_bytes(bad))
        bad = json.loads(good)
        bad["new"]["binaryPath"] = "/tmp/untrusted"
        cases.append(panel.config_bytes(bad))
        for raw in cases:
            with self.subTest(raw=raw[:100]):
                self.paths.transaction.write_bytes(raw)
                self.events.clear()
                with self.assertRaises(panel.Failure):
                    self.execute(update=True)
                self.assertEqual(self.paths.transaction.read_bytes(), raw)
                self.assertFalse(any(kind in {"build-info", "systemctl", "replace", "download"} for kind, _ in self.events))
        self.assert_data()

    def test_transaction_root_ownership_permissions_links_are_required(self):
        self.interrupted_fixture()
        for change in ({"uid": 991}, {"mode": 0o644}, {"kind": stat.S_IFLNK}, {"links": 2}):
            with self.subTest(change=change):
                previous = dict(self.files.attributes[self.paths.transaction])
                self.files.attributes[self.paths.transaction].update(change)
                with self.assertRaises(panel.Failure):
                    self.execute(update=True)
                self.files.attributes[self.paths.transaction] = previous
        self.assertFalse(any(kind in {"build-info", "systemctl", "download"} for kind, _ in self.events))

    def test_recovery_rejects_corrupt_previous_before_execution(self):
        self.interrupted_fixture()
        self.paths.previous.write_bytes(b"unknown previous binary")
        with self.assertRaisesRegex(panel.Failure, "previous binary SHA-256"):
            self.execute(update=True)
        self.assertFalse(any(kind in {"build-info", "systemctl", "download"} for kind, _ in self.events))
        self.assertTrue(self.paths.transaction.exists())

    def test_recovery_validates_previous_build_info_before_stopping(self):
        self.interrupted_fixture()
        original = self.runtime.binary_info

        def wrong_build_info(path):
            value = json.loads(original(path))
            value["version"] = "0.3.2"
            return encoded(value)

        self.runtime.binary_info = wrong_build_info
        with self.assertRaisesRegex(panel.Failure, "build-info"):
            self.execute(update=True)
        self.assert_no_stop()
        self.assertTrue(self.paths.transaction.exists())

    def test_recovery_failure_retains_marker_and_next_invocation_retries(self):
        marker = self.interrupted_fixture()
        raw = self.paths.transaction.read_bytes()
        self.runtime.fail_health.add("0.3.0")
        with self.assertRaisesRegex(panel.Failure, "transaction marker.*retained"):
            self.execute(update=True)
        self.assertEqual(self.paths.transaction.read_bytes(), raw)
        self.assertEqual(self.paths.previous.read_bytes(), binary("0.3.0"))
        self.assert_data()
        self.runtime.fail_health.clear()
        self.events.clear()
        self.assert_recovered(marker)

    def run_options(self, args):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            self.manager.execute(panel.arguments(args), SOURCE, "amd64")
        return output.getvalue()

    def test_custom_loopback_port_is_recorded_and_preserved_by_upgrade(self):
        self.run_options(["--repo", REPO, "--port", "6217", "--yes"])
        config = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(config["listen"], "127.0.0.1:6217")
        self.assertEqual(config["healthHost"], config["listen"])
        self.assertIn(b"-listen 127.0.0.1:6217 ", self.paths.unit.read_bytes())
        self.downloads.use("0.3.1")
        self.run_options(["--upgrade", "--yes"])
        updated = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(updated["listen"], config["listen"])
        self.assertEqual(updated["healthHost"], config["healthHost"])
        self.assertEqual(updated["version"], "0.3.1")

    def test_format3_selects_linux_even_when_windows_amd64_appears_first(self):
        metadata = json.loads(self.downloads.assets["release.json"])
        metadata["binaries"].insert(0, metadata["binaries"].pop())
        self.downloads.assets, self.downloads.api = refresh_release(self.downloads.assets, metadata)
        self.execute()
        self.assertEqual(self.paths.binary.read_bytes(), binary())
        self.assertFalse(any(kind == "download" and url.endswith(".zip") for kind, url in self.events))

    def test_status_without_install_or_with_pending_transactions_is_read_only(self):
        output = self.run_options(["--status"])
        self.assertIn("No recognized installation", output)
        self.assertEqual(self.events, [])
        self.install()
        self.paths.transaction.write_bytes(b"pending, deliberately not parsed by status")
        self.paths.migration.write_bytes(b"pending, deliberately not parsed by status")
        before = {path: path.read_bytes() for path in self.root.rglob("*") if path.is_file()}
        output = json.loads(self.run_options(["--status"]))
        self.assertTrue(output["pendingBinaryRecovery"])
        self.assertTrue(output["pendingManagerRecovery"])
        self.assertTrue(output["readOnly"])
        self.assertEqual(output["service"], "not probed")
        self.assertEqual(self.events, [])
        self.assertEqual(before, {path: path.read_bytes() for path in self.root.rglob("*") if path.is_file()})

    def legacy_fixture(self):
        self.install()
        # Substitute only the pinned old hash in this import-only test module;
        # production has no path/env/CLI override for its published SHA-256.
        legacy = b"#!/bin/bash\n# synthetic pinned legacy manager, never executed\n"
        patch = mock.patch.object(panel, "LEGACY_INSTALLER_SHA256", hashlib.sha256(legacy).hexdigest())
        patch.start()
        self.addCleanup(patch.stop)
        self.files.atomic_bytes(self.paths.installer, legacy, 0o755)
        old = panel.installation_config(self.paths.config.read_bytes())
        old["installerSHA256"] = panel.LEGACY_INSTALLER_SHA256
        self.files.atomic_bytes(self.paths.config, panel.config_bytes(old), 0o644)
        self.events.clear()
        return old, legacy

    def confirm_migration(self, message, unattended):
        self.assertFalse(unattended, "--yes must never skip manager migration confirmation")
        self.events.append(("migration-confirm", message))

    def test_manager_migration_changes_only_installer_digest_without_service_restart(self):
        old, legacy = self.legacy_fixture()
        untouched = {path: path.read_bytes() for path in (self.paths.binary, self.paths.unit, self.paths.updater)}
        self.runtime.confirm = self.confirm_migration
        self.run_options(["--upgrade", "--yes"])
        config = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(config, dict(old, installerSHA256=hashlib.sha256(SOURCE).hexdigest()))
        self.assertEqual(self.paths.installer.read_bytes(), SOURCE)
        self.assertEqual(self.paths.manager_previous.read_bytes(), legacy)
        self.assertFalse(self.paths.migration.exists())
        self.assertFalse(any(kind in ("systemctl", "download", "account") for kind, _ in self.events))
        marker_writes = [i for i, event in enumerate(self.events) if event == ("replace", "manager-migration.json")]
        self.assertEqual(len(marker_writes), 2)
        self.assertLess(marker_writes[0], self.events.index(("replace", "install.sh.v0.4.0.previous")))
        self.assertLess(self.events.index(("replace", "install.sh.v0.4.0.previous")), marker_writes[1])
        self.assertLess(marker_writes[1], self.events.index(("replace", "install.sh")))
        self.assertEqual(untouched, {path: path.read_bytes() for path in untouched})
        self.assert_data()

    def test_manager_migration_cancel_no_tty_or_unknown_manager_never_writes(self):
        old, legacy = self.legacy_fixture()
        self.runtime.confirm = mock.Mock(side_effect=panel.Failure("No controlling terminal; migration cancelled"))
        with self.assertRaisesRegex(panel.Failure, "No controlling terminal"):
            self.run_options(["--migrate-manager", "--yes"])
        self.assertFalse(self.paths.migration.exists())
        self.assertFalse(self.paths.manager_previous.exists())
        self.assertEqual(self.paths.installer.read_bytes(), legacy)
        self.assertFalse(any(kind in ("replace", "systemctl", "download") for kind, _ in self.events))
        unknown = b"unknown-manager"
        self.paths.installer.write_bytes(unknown)
        old["installerSHA256"] = hashlib.sha256(unknown).hexdigest()
        self.paths.config.write_bytes(panel.config_bytes(old))
        self.events.clear()
        with self.assertRaisesRegex(panel.Failure, "Unknown installed manager"):
            self.run_options(["--upgrade", "--yes"])
        self.assertFalse(any(kind in ("build-info", "replace", "systemctl", "download") for kind, _ in self.events))

    def migration_crash_fixture(self, phase="ready", switched=True, metadata_new=False):
        old, legacy = self.legacy_fixture()
        new = dict(old, installerSHA256=hashlib.sha256(SOURCE).hexdigest())
        if phase == "ready":
            self.files.atomic_bytes(self.paths.manager_previous, legacy, 0o755)
        if switched:
            self.files.atomic_bytes(self.paths.installer, SOURCE, 0o755)
        if metadata_new:
            self.files.atomic_bytes(self.paths.config, panel.config_bytes(new), 0o644)
        marker = {"format": 1, "phase": phase, "old": old, "new": new}
        self.files.atomic_bytes(self.paths.migration, panel.config_bytes(marker), 0o600)
        self.events.clear()
        return marker, legacy

    def test_manager_power_loss_ready_restores_old_script_and_record_without_execution(self):
        for metadata_new in (False, True):
            with self.subTest(metadata_new=metadata_new):
                if metadata_new:
                    # Reuse the same safe old installation with a second crash point.
                    marker["phase"] = "ready"
                    self.files.atomic_bytes(self.paths.installer, SOURCE, 0o755)
                    self.files.atomic_bytes(self.paths.config, panel.config_bytes(marker["new"]), 0o644)
                    self.files.atomic_bytes(self.paths.migration, panel.config_bytes(marker), 0o600)
                    self.events.clear()
                else:
                    marker, legacy = self.migration_crash_fixture()
                self.run_options(["--upgrade", "--yes"])
                self.assertEqual(self.paths.installer.read_bytes(), legacy)
                self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), marker["old"])
                self.assertFalse(self.paths.migration.exists())
                self.assertFalse(any(kind in ("build-info", "systemctl", "download", "confirm") for kind, _ in self.events))
                self.assert_data()

    def test_manager_prepared_without_backup_recovers_old_but_cannot_authorize_switch(self):
        marker, legacy = self.migration_crash_fixture(phase="prepared", switched=False)
        self.run_options(["--upgrade", "--yes"])
        self.assertEqual(self.paths.installer.read_bytes(), legacy)
        self.assertFalse(self.paths.manager_previous.exists())
        self.files.atomic_bytes(self.paths.installer, SOURCE, 0o755)
        self.files.atomic_bytes(self.paths.migration, panel.config_bytes(marker), 0o600)
        self.events.clear()
        with self.assertRaisesRegex(panel.Failure, "backup phase"):
            self.run_options(["--upgrade", "--yes"])
        self.assertTrue(self.paths.migration.exists())
        self.assertFalse(any(kind in ("replace", "systemctl", "download") for kind, _ in self.events))

    def test_manager_recovery_rejects_tampered_backup_path_or_fixed_fields(self):
        marker, legacy = self.migration_crash_fixture()
        self.paths.manager_previous.write_bytes(b"corrupt backup")
        with self.assertRaisesRegex(panel.Failure, "backup SHA-256"):
            self.run_options(["--upgrade", "--yes"])
        self.paths.manager_previous.write_bytes(legacy)
        for change in ({"path": "/tmp/untrusted"}, {"format": True}, {"phase": "unknown"}):
            with self.subTest(change=change):
                self.paths.migration.write_bytes(panel.config_bytes(dict(marker, **change)))
                with self.assertRaises(panel.Failure):
                    self.run_options(["--upgrade", "--yes"])
        bad = json.loads(panel.config_bytes(marker))
        bad["new"]["version"] = "0.9.0"
        self.paths.migration.write_bytes(panel.config_bytes(bad))
        with self.assertRaisesRegex(panel.Failure, "only installerSHA256"):
            self.run_options(["--upgrade", "--yes"])
        self.assertFalse(any(kind in ("replace", "systemctl", "download", "build-info") for kind, _ in self.events))

    def test_manager_metadata_write_failure_rolls_back_and_old_binary_transaction_precedes_migration(self):
        old, legacy = self.legacy_fixture()
        self.runtime.confirm = self.confirm_migration
        original = self.files.atomic_bytes
        once = [True]

        def fail_metadata(path, raw, mode):
            if path == self.paths.config and hashlib.sha256(SOURCE).hexdigest().encode() in raw and once[0]:
                once[0] = False
                raise OSError("injected manager metadata write failure")
            return original(path, raw, mode)

        self.files.atomic_bytes = fail_metadata
        with self.assertRaisesRegex(panel.Failure, "rollback succeeded"):
            self.run_options(["--upgrade", "--yes"])
        self.assertEqual(self.paths.installer.read_bytes(), legacy)
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), old)
        self.assertFalse(self.paths.migration.exists())
        self.files.atomic_bytes = original
        new = dict(old, version="0.3.1", binarySHA256=hashlib.sha256(binary("0.3.1")).hexdigest())
        self.files.atomic_bytes(self.paths.transaction, panel.config_bytes({"format": 1, "phase": "prepared", "old": old, "new": new}), 0o600)
        self.events.clear()
        self.run_options(["--upgrade", "--yes"])
        self.assertFalse(self.paths.transaction.exists())
        self.assertEqual(self.paths.installer.read_bytes(), legacy)
        self.assertFalse(any(kind in ("migration-confirm", "download") for kind, _ in self.events))
        self.assertIn(("healthy", old["version"]), self.events)
        self.assert_data()

    def test_update_requires_recognized_metadata(self):
        with self.assertRaisesRegex(panel.Failure, "No recognized installation"):
            self.execute(update=True)
        self.assert_no_stop()


class ArchiveTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="panel-archive-test-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.candidate = self.root / "panel"
        self.raw = binary()
        self.expected = {"size": len(self.raw), "sha256": hashlib.sha256(self.raw).hexdigest()}
        self.tar = gzip.decompress(archive(self.raw))

    def tearDown(self):
        self.assertTrue(all(path.name == "panel" for path in self.root.iterdir()))

    def extract(self, packed, expected=None):
        if self.candidate.exists() or self.candidate.is_symlink():
            self.candidate.unlink()
        return panel.extract_panel(io.BytesIO(packed), self.candidate, expected or self.expected)

    def modified_header(self, mutate):
        header = bytearray(self.tar[:512])
        mutate(header)
        header[148:156] = b" " * 8
        header[148:156] = ("%06o\0 " % sum(header)).encode("ascii")
        return gzip.compress(bytes(header) + self.tar[512:], mtime=0)

    def test_exact_archive_extracts_fixed_panel_and_returns_elf_identity(self):
        self.assertEqual(self.extract(archive(self.raw)), (len(self.raw), self.expected["sha256"]))
        self.assertEqual(self.candidate.read_bytes(), self.raw)
        self.assertEqual(len(list(self.root.iterdir())), 1)

    def test_path_traversal_absolute_alias_and_prefix_are_forbidden(self):
        for name in (b"../panel", b"/panel", b"./panel", b"dir/panel", b"C:\\panel", b"panel/", b"panel\0hidden", b"p\xffnel"):
            with self.subTest(name=name):
                packed = self.modified_header(lambda header: header.__setitem__(slice(0, 100), name.ljust(100, b"\0")))
                with self.assertRaises(panel.Failure):
                    self.extract(packed)
                self.assertFalse(self.candidate.exists())
        packed = self.modified_header(lambda header: header.__setitem__(slice(345, 500), b"../escape".ljust(155, b"\0")))
        with self.assertRaises(panel.Failure):
            self.extract(packed)
        self.assertFalse(self.candidate.exists())

    def test_link_special_extension_and_directory_headers_are_forbidden(self):
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.DIRTYPE, tarfile.CHRTYPE, tarfile.BLKTYPE, tarfile.FIFOTYPE,
                     tarfile.XHDTYPE, tarfile.XGLTYPE, tarfile.GNUTYPE_LONGNAME, tarfile.GNUTYPE_LONGLINK, tarfile.GNUTYPE_SPARSE, b"7", b"Z"):
            with self.subTest(kind=kind):
                packed = self.modified_header(lambda header: header.__setitem__(slice(156, 157), kind))
                with self.assertRaises(panel.Failure):
                    self.extract(packed)
                self.assertFalse(self.candidate.exists())
        packed = self.modified_header(lambda header: header.__setitem__(slice(157, 257), b"/etc/passwd".ljust(100, b"\0")))
        with self.assertRaises(panel.Failure):
            self.extract(packed)

    def test_only_mode_0755_regular_panel_and_valid_header_are_accepted(self):
        for mode in (0o644, 0o777, 0o4755, 0o2755):
            with self.subTest(mode=mode):
                packed = self.modified_header(lambda header: header.__setitem__(slice(100, 108), ("%07o\0" % mode).encode()))
                with self.assertRaisesRegex(panel.Failure, "0755"):
                    self.extract(packed)
        bad = bytearray(self.tar)
        bad[100] ^= 1
        with self.assertRaisesRegex(panel.Failure, "tar header"):
            self.extract(gzip.compress(bad, mtime=0))
        packed = self.modified_header(lambda header: header.__setitem__(slice(257, 265), b"gnu     "))
        with self.assertRaisesRegex(panel.Failure, "USTAR"):
            self.extract(packed)

    def test_additional_members_duplicates_and_nonzero_padding_are_forbidden(self):
        data_end = 512 + len(self.raw)
        block_end = 512 + ((len(self.raw) + 511) // 512) * 512
        second = bytearray(self.tar[:block_end])
        for raw in (self.tar[:block_end] + second + b"\0" * 1024,
                    self.tar + second,
                    self.tar[:data_end] + b"x" + self.tar[data_end + 1:],
                    self.tar[:-1] + b"x"):
            with self.subTest(size=len(raw)), self.assertRaises(panel.Failure):
                self.extract(gzip.compress(raw, mtime=0))

    def test_truncated_tar_missing_end_blocks_and_partial_trailer_are_forbidden(self):
        block_end = 512 + ((len(self.raw) + 511) // 512) * 512
        for length in (0, 100, 511, 512, 512 + len(self.raw) - 1, block_end, block_end + 512, len(self.tar) - 1):
            with self.subTest(length=length), self.assertRaisesRegex(panel.Failure, "Truncated tar"):
                self.extract(gzip.compress(self.tar[:length], mtime=0))

    def test_gzip_integrity_truncation_concatenation_and_trailing_bytes(self):
        good = archive(self.raw)
        crc, length = bytearray(good), bytearray(good)
        crc[-8] ^= 1
        length[-4] ^= 1
        for packed in (b"not gzip", good[:-1], good[:10], bytes(crc), bytes(length), good + good, good + b"\0", good + b"trailing"):
            with self.subTest(size=len(packed)), self.assertRaises(panel.Failure):
                self.extract(packed)

    def test_inner_hash_size_and_global_binary_limit_are_separate(self):
        for changes in ({"sha256": "f" * 64}, {"size": len(self.raw) - 1}, {"size": len(self.raw) + 1}):
            with self.subTest(changes=changes), self.assertRaises(panel.Failure):
                self.extract(archive(self.raw), dict(self.expected, **changes))
        with mock.patch.object(panel, "BINARY_LIMIT", len(self.raw) - 1):
            with self.assertRaisesRegex(panel.Failure, "size"):
                self.extract(archive(self.raw))

    def test_expansion_and_compressed_limits_are_enforced_before_full_allocation(self):
        class Counted(io.BytesIO):
            def __init__(self, raw):
                super().__init__(raw)
                self.largest_read = 0

            def read(self, size=-1):
                self.assert_bounded(size)
                self.largest_read = max(self.largest_read, size)
                return super().read(size)

            @staticmethod
            def assert_bounded(size):
                if not 0 < size <= 65536:
                    raise AssertionError("Unbounded compressed read")

        stream = Counted(gzip.compress(b"\0" * (2 * 1024 * 1024), mtime=0))
        with mock.patch.object(panel, "TAR_LIMIT", 32768):
            with self.assertRaisesRegex(panel.Failure, "expansion"):
                list(panel.gzip_chunks(stream))
        good = archive(self.raw)
        with mock.patch.object(panel, "ARCHIVE_LIMIT", len(good) - 1):
            with self.assertRaisesRegex(panel.Failure, "compressed size"):
                self.extract(good)

    def test_destination_is_exclusive_and_never_follows_existing_symlink(self):
        self.candidate.write_bytes(b"existing trusted bytes")
        with self.assertRaises(FileExistsError):
            panel.extract_panel(io.BytesIO(archive(self.raw)), self.candidate, self.expected)
        self.assertEqual(self.candidate.read_bytes(), b"existing trusted bytes")
        self.candidate.unlink()
        target = self.root / "outside"
        try:
            self.candidate.symlink_to(target)
        except OSError:
            return  # Windows may not grant symlink privileges; exclusive-open tested above.
        with self.assertRaises(OSError):
            panel.extract_panel(io.BytesIO(archive(self.raw)), self.candidate, self.expected)
        self.assertFalse(target.exists())


class ValidationTests(unittest.TestCase):
    def test_confirmation_uses_separate_nonseekable_tty_streams(self):
        class NonSeekableTTY(io.RawIOBase):
            def __init__(self, mode):
                super().__init__()
                self.mode, self.answer, self.written = mode, bytearray(b"yes\n"), bytearray()

            def readable(self):
                return self.mode == "r"

            def writable(self):
                return self.mode == "w"

            def seekable(self):
                return False

            def seek(self, *_args):
                raise AssertionError("TTY confirmation must never seek")

            def tell(self):
                raise io.UnsupportedOperation("non-seekable TTY")

            def readinto(self, buffer):
                count = min(len(buffer), len(self.answer))
                buffer[:count] = self.answer[:count]
                del self.answer[:count]
                return count

            def write(self, data):
                self.written.extend(data)
                return len(data)

        streams, calls = [], []

        def open_tty(path, mode, encoding):
            self.assertEqual(path, "/dev/tty")
            self.assertIn(mode, ("w", "r"))
            if mode == "r":
                self.assertEqual(bytes(streams[0].written), b"Type yes to continue: ")
            calls.append(mode)
            raw = NonSeekableTTY(mode)
            streams.append(raw)
            buffer = io.BufferedWriter(raw) if mode == "w" else io.BufferedReader(raw)
            return io.TextIOWrapper(buffer, encoding=encoding)

        with mock.patch.object(panel, "open", side_effect=open_tty, create=True), contextlib.redirect_stdout(io.StringIO()):
            panel.Runtime().confirm("Confirm a test operation", False)
        self.assertEqual(calls, ["w", "r"])

    def test_confirmation_without_tty_requires_explicit_yes(self):
        with mock.patch.object(panel, "open", side_effect=OSError("no controlling terminal"), create=True) as opener, \
                contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(panel.Failure, "No controlling terminal"):
                panel.Runtime().confirm("Confirm a test operation", False)
            opener.assert_called_once_with("/dev/tty", "w", encoding="utf-8")
            opener.reset_mock()
            panel.Runtime().confirm("Explicit unattended test", True)
            opener.assert_not_called()

    def test_wrapper_matches_embedded_reviewed_copy(self):
        self.assertEqual((SCRIPTS / "update.sh").read_bytes(), panel.WRAPPER)
        self.assertIn(b"/bin/bash -p /usr/local/lib/server-panel/install.sh --update", panel.WRAPPER)

    def test_stable_version_limits(self):
        self.assertEqual(panel.version_tuple("v2147483647.0.42", True), (2147483647, 0, 42))
        for value in ("0.3.0", "v01.3.0", "v1.2", "v1.2.3-rc1", "v1.2.3+build", "v2147483648.0.0", "v1.2.3\n", "v１.2.3", [], True):
            with self.subTest(value=value), self.assertRaises(panel.Failure):
                panel.version_tuple(value, True)

    def test_conservative_repo_boundary(self):
        self.assertEqual(panel.repository("A-b/R_e.po-1"), "A-b/R_e.po-1")
        for value in ("https://github.com/a/b", "a/b/c", "a.b/repo", "a_b/repo", "../repo", "-a/b", "a/.b", "a/b-", "a/b?x", "a/b%2f", "a/" + "x" * 101, "ａ/b", "a/b\n"):
            with self.subTest(value=value), self.assertRaises(panel.Failure):
                panel.repository(value)

    def test_origin_normalization_and_injection_rejection(self):
        self.assertEqual(panel.origin("https://PANEL.example.com:8443/"), ("https://panel.example.com:8443", "panel.example.com:8443"))
        self.assertEqual(panel.origin(""), ("", "127.0.0.1:5173"))
        for value in ("http://panel.example.com", "https://127.0.0.1", "https://localhost", "https://user@panel.example.com", "https://panel.example.com/x",
                      "https://panel.example.com?", "https://panel.example.com#", "https://panel.example.com:%i", "https://panel.example.com:0", "https://panel.example.com:\n443",
                      "https://panel.example.com\nExecStart=x", "https://panel.example.com:", "https://bad_.example.com", "https://[::1]"):
            with self.subTest(value=value), self.assertRaises(panel.Failure):
                panel.origin(value)

    def test_cli_rejects_unknown_options_and_update_source_overrides(self):
        for argv in (["--update", "--repo", REPO], ["--update", "--origin", "https://example.com"], ["--repo", REPO, "--repo", REPO],
                     ["--repo", REPO, "--version", "v1.0.0", "--version", "v1.0.1"], ["--update", "--update"]):
            with self.subTest(argv=argv), self.assertRaises(panel.Failure):
                panel.arguments(argv)
        for flag in ("--url", "--unit", "--root", "--hook", "--force", "--rep"):
            with self.subTest(flag=flag), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                panel.arguments(["--repo", REPO, flag, "arbitrary"])
        with contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as result:
            panel.arguments(["--help"])
        self.assertEqual(result.exception.code, 0)

    def test_update_source_cannot_be_user_selected_path(self):
        with self.assertRaisesRegex(panel.Failure, "fixed root-owned"):
            panel.read_source("/tmp/downloaded/install.sh", panel.Paths.installer, panel.SafeFiles(), True)

    def test_duplicate_json_nonfinite_and_extra_manifest_fields_rejected(self):
        for raw in (b'{"a":1,"a":2}', b'{"x":NaN}', b'{"x":Infinity}', b'[]', b'\xff'):
            with self.subTest(raw=raw), self.assertRaises(panel.Failure):
                panel.strict_json(raw, 100)
        assets, api = release()
        value = json.loads(assets["release.json"])
        for changes in ({"format": True}, {"dataSchema": 2}, {"tag": "v0.3.1"}, {"repository": "other/repo"}, {"hook": "do-not-run"},
                        {"commit": "unknown"}, {"buildTime": "2026-02-30T00:00:00Z"}):
            with self.subTest(changes=changes):
                candidate = dict(value, **changes)
                raw = encoded(candidate)
                sums = panel.checksums(assets["SHA256SUMS"])
                sums["release.json"] = hashlib.sha256(raw).hexdigest()
                with self.assertRaises(panel.Failure):
                    panel.manifest(raw, sums, REPO, "v0.3.0")
        raw = assets["release.json"].replace(b'"format":3', b'"format":3,"format":3')
        sums = panel.checksums(assets["SHA256SUMS"])
        sums["release.json"] = hashlib.sha256(raw).hexdigest()
        with self.assertRaisesRegex(panel.Failure, "Duplicate"):
            panel.manifest(raw, sums, REPO, "v0.3.0")

    def test_binary_manifest_platform_size_and_checksum_fields(self):
        assets, _ = release()
        for change in ({"name": "../../panel"}, {"arch": "386"}, {"os": "windows"}, {"size": True}, {"size": panel.BINARY_LIMIT + 1}, {"sha256": "not-a-hash"}):
            with self.subTest(change=change):
                value = json.loads(assets["release.json"])
                value["binaries"][0].update(change)
                raw = encoded(value)
                sums = panel.checksums(assets["SHA256SUMS"])
                sums["release.json"] = hashlib.sha256(raw).hexdigest()
                with self.assertRaises(panel.Failure):
                    panel.manifest(raw, sums, REPO, "v0.3.0")

    def test_manifest_format1_bare_assets_and_archive_injection_are_rejected(self):
        assets, _ = release()
        for changes in ({"name": "../../panel.tar.gz"}, {"name": "panel-linux-amd64"}, {"size": True}, {"size": 0},
                        {"size": panel.ARCHIVE_LIMIT + 1}, {"sha256": "f" * 64}, {"sha256": "F" * 64}, {"path": "panel"}):
            with self.subTest(changes=changes):
                value = json.loads(assets["release.json"])
                value["binaries"][0]["archive"].update(changes)
                raw = encoded(value)
                sums = panel.checksums(assets["SHA256SUMS"])
                sums["release.json"] = hashlib.sha256(raw).hexdigest()
                with self.assertRaises(panel.Failure):
                    panel.manifest(raw, sums, REPO, "v0.3.0")
        value = json.loads(assets["release.json"])
        value["format"] = 1
        for item in value["binaries"]:
            item["name"] = "panel-linux-" + item["arch"]
            del item["archive"]
        raw = encoded(value)
        sums = panel.checksums(assets["SHA256SUMS"])
        sums["release.json"] = hashlib.sha256(raw).hexdigest()
        with self.assertRaisesRegex(panel.Failure, "format 3"):
            panel.manifest(raw, sums, REPO, "v0.3.0")

    def test_checksums_never_cover_inner_panel_and_archives_have_distinct_identity(self):
        assets, _ = release()
        sums = panel.checksums(assets["SHA256SUMS"])
        self.assertNotIn("panel", sums)
        metadata = panel.manifest(assets["release.json"], sums, REPO, "v0.3.0")
        for item in metadata["binaries"]:
            self.assertEqual(item["name"], "panel.exe" if item["os"] == "windows" else "panel")
            self.assertEqual(item["archive"]["sha256"], sums[item["archive"]["name"]])
            self.assertNotEqual(item["sha256"], item["archive"]["sha256"])
        with self.assertRaises(panel.Failure):
            panel.checksums(assets["SHA256SUMS"] + b"a" * 64 + b"  panel\n")

    def test_checksums_are_exact_and_cover_all_assets(self):
        assets, _ = release()
        good = assets["SHA256SUMS"]
        self.assertEqual(set(panel.checksums(good)), panel.NAMES - {"SHA256SUMS"})
        for raw in (good + good.splitlines(keepends=True)[0], good.replace(b"  release.json", b"  ../release.json"), good.replace(b"  ", b" *", 1),
                    good.replace(b"\n", b"\r\n"), good[:-1], b"", b"f" * (panel.SUMS_LIMIT + 1)):
            with self.subTest(raw=raw[:80]), self.assertRaises(panel.Failure):
                panel.checksums(raw)

    def test_api_requires_complete_canonical_stable_release(self):
        _, api = release()
        self.assertEqual(panel.release_api(encoded(api), REPO, None)[0], "v0.3.0")
        for changes in ({"draft": True}, {"prerelease": True}, {"tag_name": "v0.3.0-rc1"}, {"assets": api["assets"][:-1]},
                        {"html_url": "https://evil.example/release"}):
            with self.subTest(changes=changes), self.assertRaises(panel.Failure):
                panel.release_api(encoded(dict(api, **changes)), REPO, None)
        bad = json.loads(encoded(api))
        bad["assets"][0]["browser_download_url"] = "https://evil.example/binary"
        with self.assertRaisesRegex(panel.Failure, "Non-canonical"):
            panel.release_api(encoded(bad), REPO, None)
        with self.assertRaisesRegex(panel.Failure, "wrong release tag"):
            panel.release_api(encoded(api), REPO, "v0.3.1")

    def test_build_info_checks_every_identity_field(self):
        assets, _ = release()
        info = json.loads(binary()[64:])
        metadata = json.loads(assets["release.json"])
        self.assertEqual(panel.build_info(encoded(info), REPO, "amd64", metadata), info)
        for changes in ({"dataSchema": 2}, {"dataSchema": True}, {"version": "0.3.1"}, {"arch": "arm64"}, {"os": "windows"},
                        {"repository": "other/repo"}, {"commit": "b" * 40}, {"buildTime": "2026-11-07T00:00:00Z"}):
            with self.subTest(changes=changes), self.assertRaises(panel.Failure):
                panel.build_info(encoded(dict(info, **changes)), REPO, "amd64", metadata)

    def test_new_cli_default_repository_port_status_and_mutation_conflicts(self):
        self.assertEqual(panel.arguments(["--yes"]).repo, panel.DEFAULT_REPO)
        self.assertEqual(panel.arguments(["--port", "6217"]).port, 6217)
        self.assertTrue(panel.arguments(["--upgrade"]).upgrade)
        self.assertTrue(panel.arguments(["--migrate-manager"]).migrate_manager)
        self.assertTrue(panel.arguments(["--status"]).status)
        for argv in (["--status", "--yes"], ["--status", "--version=v0.5.0"], ["--upgrade", "--port=6217"],
                     ["--upgrade", "--repo=a/b"], ["--upgrade", "--update"], ["--migrate-manager", "--origin=https://example.com"],
                     ["--port=80"], ["--port=65536"], ["--port=06217"], ["--port=6217", "--port=6218"]):
            with self.subTest(argv=argv), self.assertRaises(panel.Failure):
                panel.arguments(argv)

    def test_main_status_returns_before_preflight_trusted_source_or_umask(self):
        manager = mock.Mock()
        with mock.patch.object(panel, "Manager", return_value=manager), \
                mock.patch.object(panel.sys, "argv", ["-", "reviewed.sh", "--status"]), \
                mock.patch.object(panel, "read_source", side_effect=AssertionError("status read source")), \
                mock.patch.object(panel.os, "umask", side_effect=AssertionError("status changed umask")):
            panel.main()
        manager.status.assert_called_once_with()
        manager.runtime.preflight.assert_not_called()
        manager.execute.assert_not_called()

    def test_upgrade_and_migration_use_trusted_local_source_validation(self):
        for flag in ("--upgrade", "--migrate-manager"):
            with self.subTest(flag=flag):
                manager = mock.Mock()
                manager.paths.installer = panel.Paths.installer
                with mock.patch.object(panel, "Manager", return_value=manager), \
                        mock.patch.object(panel.sys, "argv", ["-", "/reviewed/install.sh", flag]), \
                        mock.patch.object(panel, "read_source", side_effect=panel.Failure("untrusted source")) as reader:
                    with self.assertRaisesRegex(panel.Failure, "untrusted source"):
                        panel.main()
                reader.assert_called_once_with("/reviewed/install.sh", panel.Paths.installer, manager.files, False)
                manager.execute.assert_not_called()

    def test_legacy_template_and_wrapper_identity_remain_unchanged(self):
        self.assertEqual(hashlib.sha256(panel.WRAPPER).hexdigest(), panel.LEGACY_WRAPPER_SHA256)
        config = {"repository": REPO, "listen": panel.LISTEN, "origin": "", "healthHost": panel.LISTEN}
        self.assertEqual(panel.unit_bytes(config), panel.legacy_unit_bytes(config))
        self.assertEqual(SOURCE.splitlines()[1], b"# QQON-Panel interactive installer protocol: 3")
        self.assertEqual(panel.LEGACY_INSTALLER_SHA256, "bc78f2ec682704bb5beb965806ec56e511827f6d3451949a03d5b40db535eb91")

    def test_unit_preserves_deployment_sandboxing(self):
        unit = panel.unit_bytes({"repository": REPO, "listen": panel.LISTEN, "origin": "", "healthHost": panel.LISTEN}).decode()
        for setting in ("User=server-panel", "Group=server-panel", "NoNewPrivileges=true", "ProtectSystem=strict", "ProtectHome=true",
                        "PrivateTmp=true", "ProtectKernelTunables=true", "ProtectKernelModules=true", "ProtectControlGroups=true",
                        "RestrictSUIDSGID=true", "LockPersonality=true", "RestrictRealtime=true", "RestrictNamespaces=true", "CapabilityBoundingSet=",
                        "StateDirectoryMode=0700", "UMask=0077", "MemoryMax=128M", "RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX"):
            self.assertIn(setting, unit)
        self.assertNotIn("%", unit)
        self.assertNotIn(" -origin ", unit)


class Response:
    def __init__(self, raw, url, headers=None, status=200):
        self.data = io.BytesIO(raw)
        self.url, self.headers, self.status = url, headers or {}, status

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def geturl(self):
        return self.url

    def read1(self, size):
        return self.data.read(size)


class NetworkTests(unittest.TestCase):
    def test_download_boundaries_length_encoding_and_final_host(self):
        url = panel.canonical(REPO, "v0.3.0", "release.json")
        cases = ((b"1234", {}, url, 3), (b"1", {"Content-Length": "4"}, url, 3),
                 (b"12", {"Content-Length": "3"}, url, 3), (b"1", {"Content-Encoding": "gzip"}, url, 3),
                 (b"1", {}, "https://evil.example/file", 3))
        for raw, headers, final, limit in cases:
            with self.subTest(headers=headers, final=final, limit=limit):
                opener = mock.Mock()
                opener.open.return_value = Response(raw, final, headers)
                download = panel.Downloads(opener=opener, timer=lambda _: contextlib.nullcontext())
                with self.assertRaises(panel.Failure):
                    download.get(url, limit, 120)
        opener = mock.Mock()
        opener.open.return_value = Response(b"ok", "https://release-assets.githubusercontent.com/signed?token=temporary", {"Content-Length": "2"})
        self.assertEqual(panel.Downloads(opener, lambda _: contextlib.nullcontext()).get(url, 2, 120), b"ok")
        self.assertEqual(opener.open.call_args.kwargs["timeout"], 15)

    def test_redirects_never_escape_allowlist_or_exceed_five(self):
        start = panel.canonical(REPO, "v0.3.0", "panel-linux-amd64.tar.gz")
        for bad in ("http://objects.githubusercontent.com/file", "https://evil.example/file", "https://objects.githubusercontent.com.evil.example/file",
                    "https://user@objects.githubusercontent.com/file", "https://objects.githubusercontent.com:444/file", "https://objects.githubusercontent.com/file#x",
                    "https://github.com/other/repo/releases/download/v0.3.0/panel-linux-amd64", "file:///etc/passwd"):
            with self.subTest(url=bad), self.assertRaises(panel.Failure):
                panel.validate_url(bad, start)
        handler = panel.Redirects(start, False)
        request = panel.urllib.request.Request(start)
        for number in range(5):
            request = handler.redirect_request(request, None, 302, "", {}, "https://objects.githubusercontent.com/" + str(number))
        with self.assertRaisesRegex(panel.Failure, "Too many"):
            handler.redirect_request(request, None, 302, "", {}, "https://objects.githubusercontent.com/6")
        api = "https://api.github.com/repos/" + REPO + "/releases/latest"
        with self.assertRaisesRegex(panel.Failure, "API redirect"):
            panel.Redirects(api, True).redirect_request(panel.urllib.request.Request(api), None, 302, "", {}, api)

    def test_api_time_limit_and_asset_time_limit_cannot_expand(self):
        opener = mock.Mock()
        download = panel.Downloads(opener, lambda _: contextlib.nullcontext())
        for url, timeout, is_api in (("https://api.github.com/repos/a/b/releases/latest", 16, True),
                                     (panel.canonical(REPO, "v0.3.0", "release.json"), 121, False)):
            with self.subTest(timeout=timeout), self.assertRaises(panel.Failure):
                download.get(url, 100, timeout, api=is_api)
        opener.open.assert_not_called()

    def test_missing_unit_nonzero_show_is_safe_for_initial_install(self):
        runtime = panel.Runtime()
        runtime.run = mock.Mock(return_value=(1, b"LoadState=not-found\nFragmentPath=\nDropInPaths=\n"))
        runtime.inspect_unit(None)
        runtime.run.return_value = (0, b"LoadState=loaded\nFragmentPath=/usr/lib/systemd/system/unknown.service\nDropInPaths=\n")
        with self.assertRaises(panel.Failure):
            runtime.inspect_unit(panel.Paths.unit)

    def test_termination_signal_is_not_swallowed_by_health_retry(self):
        connection = mock.Mock()
        connection.request.side_effect = panel.Interrupted("signal")
        with mock.patch.object(panel.http.client, "HTTPConnection", return_value=connection), \
                mock.patch.object(panel, "deadline", return_value=contextlib.nullcontext()):
            with self.assertRaises(panel.Interrupted):
                panel.Runtime().health_once({"listen": panel.LISTEN, "healthHost": panel.LISTEN, "version": "0.3.0"}, 2)
        connection.close.assert_called_once()

    def test_health_is_loopback_only_and_sends_configured_host(self):
        response = mock.Mock(status=200)
        response.read.return_value = encoded({"status": "ok", "version": "0.3.1", "dataSchema": 1})
        connection = mock.Mock()
        connection.getresponse.return_value = response
        config = {"listen": "127.0.0.1:6273", "healthHost": "panel.example.com:8443", "version": "0.3.1"}
        with mock.patch.object(panel.http.client, "HTTPConnection", return_value=connection) as factory, \
                mock.patch.object(panel, "deadline", return_value=contextlib.nullcontext()):
            self.assertTrue(panel.Runtime().health_once(config, 2))
            factory.assert_called_once_with("127.0.0.1", 6273, timeout=2)
            connection.request.assert_called_once_with("GET", "/healthz", headers={"Host": "panel.example.com:8443", "Connection": "close"})
            response.read.return_value = encoded({"status": "ok", "version": "0.3.0", "dataSchema": 1})
            self.assertFalse(panel.Runtime().health_once(config, 2))
            response.read.return_value = encoded({"status": "ok", "version": "0.3.1", "dataSchema": True})
            self.assertFalse(panel.Runtime().health_once(config, 2))


if __name__ == "__main__":
    # Fail closed if a future test accidentally stops using the fake adapters.
    with mock.patch.object(panel.subprocess, "Popen", side_effect=AssertionError("Real subprocesses are forbidden in installer tests")), \
            mock.patch.object(panel.urllib.request, "urlopen", side_effect=AssertionError("Real network is forbidden in installer tests")):
        unittest.main(verbosity=2)
