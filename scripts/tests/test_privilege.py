"""Linux privilege transitions use temporary bytes and mocked service identities only."""
import contextlib
import hashlib
import importlib.util
import io
from pathlib import Path
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("installer_privilege_fixture", Path(__file__).with_name("test_install.py"))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
panel = fixture.panel


class PrivilegeTests(unittest.TestCase):
    def setUp(self):
        self.base = fixture.InstallationTests()
        self.base.setUp()
        self.addCleanup(self.base.doCleanups)
        self.paths, self.files = self.base.paths, self.base.files
        self.runtime, self.manager = self.base.runtime, self.base.manager
        self.base.downloads.use("0.7.0")

    def install(self):
        self.base.install()
        for name in ("accounts.json", ".panel.lock"):
            self.files.owner_mode(self.paths.data / name, 991, 991, 0o600)
        self.runtime.prompt = mock.Mock(return_value="同意最高权限")
        return panel.installation_config(self.paths.config.read_bytes())

    def run_privilege(self, *flags):
        return self.base.run_options(["--manage", "privilege", *flags])

    def test_yes_is_not_consent_and_never_stops_service(self):
        old = self.install()
        with self.assertRaisesRegex(panel.Failure, "未同意最高权限"):
            self.run_privilege("--yes")
        self.runtime.prompt.assert_not_called()
        self.assertFalse(self.paths.consent.exists())
        self.assertFalse(self.paths.privilege_transaction.exists())
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), old)
        self.base.assert_no_stop()

    def test_explicit_migration_preserves_account_and_changes_all_owners(self):
        old = self.install()
        self.run_privilege("--accept-system-management", "--yes")
        new = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(new["privilegeMode"], "system")
        self.assertEqual(new["privilegeConsent"], panel.strict_json(self.paths.consent.read_bytes(), 4096))
        for key in old:
            if key not in ("format", *panel.PRIVILEGE_FIELDS):
                self.assertEqual(old[key], new[key])
        for path in (self.paths.data, self.paths.data / "accounts.json", self.paths.data / ".panel.lock"):
            self.assertEqual(self.files.info(path).st_uid, 0)
            self.assertEqual(self.files.info(path).st_gid, 0)
        unit = self.paths.unit.read_text()
        self.assertIn("User=root\nGroup=root\n", unit)
        self.assertNotIn("MemoryMax=128M", unit)
        self.assertIn("GOMEMLIMIT=64MiB", unit)
        self.assertFalse(self.paths.privilege_transaction.exists())
        self.assertTrue(list(self.paths.backups.glob("backup-*.tar")))
        self.base.assert_data()
        self.assertTrue(self.runtime.active)

    def test_interactive_confirmation_requires_exact_phrase(self):
        self.install()
        for answer in ("yes", "Y", "同意", ""):
            self.runtime.prompt.return_value = answer
            with self.assertRaises(panel.Failure):
                self.run_privilege()
            self.assertFalse(self.paths.privilege_transaction.exists())
        self.runtime.prompt.return_value = "同意最高权限"
        self.run_privilege()
        self.assertTrue(self.paths.consent.exists())

    def test_failed_root_start_restores_identity_and_original_data(self):
        old = self.install()
        healthy = self.runtime.healthy
        def fail_root(config):
            if panel.system_mode(config):
                raise panel.Failure("injected root startup failure")
            return healthy(config)
        self.runtime.healthy = fail_root
        with self.assertRaisesRegex(panel.Failure, "已恢复受限身份"):
            self.run_privilege("--accept-system-management", "--yes")
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), old)
        self.assertEqual(self.files.info(self.paths.data).st_uid, 991)
        self.assertEqual(self.files.info(self.paths.data / "accounts.json").st_uid, 991)
        self.assertFalse(self.paths.consent.exists())
        self.assertFalse(self.paths.privilege_transaction.exists())
        self.assertTrue(self.runtime.active)
        self.base.assert_data()

    def test_rollback_restores_new_runtime_file_to_old_identity(self):
        self.install()
        healthy = self.runtime.healthy
        def fail_after_file(config):
            if panel.system_mode(config):
                extra = self.paths.data / "startup-created.json"
                extra.write_bytes(b"bounded runtime state")
                self.files.owner_mode(extra, 0, 0, 0o600)
                raise panel.Failure("injected startup failure")
            healthy(config)
        self.runtime.healthy = fail_after_file
        with self.assertRaises(panel.Failure):
            self.run_privilege("--accept-system-management", "--yes")
        self.assertEqual(self.files.info(self.paths.data / "startup-created.json").st_uid, 991)
        self.assertFalse(self.paths.consent.exists())

    def test_stopped_service_remains_stopped_after_verification(self):
        self.install()
        self.runtime.active = False
        self.run_privilege("--accept-system-management", "--yes")
        self.assertFalse(self.runtime.active)
        self.assertTrue(self.paths.consent.exists())

    def test_initial_install_requires_separate_flag(self):
        self.base.run_options(["--repo", fixture.REPO, "--mode", "local", "--yes", "--accept-system-management"])
        new = panel.installation_config(self.paths.config.read_bytes())
        self.assertEqual(new["privilegeMode"], "system")
        self.assertIsNone(self.runtime.identity, "new root install should not create unused service account")
        self.assertEqual(self.files.info(self.paths.data).st_uid, 0)
        self.assertTrue(self.paths.consent.exists())

    def test_ordinary_update_flags_cannot_grant_permission(self):
        for argv in (["--update", "--accept-system-management"], ["--upgrade", "--accept-system-management"],
                     ["--migrate-manager", "--accept-system-management"], ["--status", "--accept-system-management"],
                     ["--manage", "update", "--accept-system-management"], ["--manage", "start", "--accept-system-management"]):
            with self.subTest(argv=argv), self.assertRaises(panel.Failure):
                panel.arguments(argv)
        old = self.install()
        new = dict(old, version="0.8.0", binarySHA256="a" * 64, privilegeMode="system",
                   privilegeConsent={"format": 1, "mode": "system", "identity": "root", "noticeVersion": 1,
                       "acceptedAt": "2026-11-07T00:00:00Z", "acceptedBy": "root", "notice": "full-system-management"})
        with self.assertRaisesRegex(panel.Failure, "普通升级"):
            panel.update_transaction(panel.config_bytes({"format": 1, "phase": "prepared", "old": old, "new": new}))

    def test_v06_manager_migration_preserves_qqon_and_stays_restricted(self):
        self.install()
        old = panel.installation_config(self.paths.config.read_bytes())
        old = {key: value for key, value in old.items() if key not in panel.PRIVILEGE_FIELDS}
        legacy = b"synthetic pinned v06 manager never executed"
        with mock.patch.object(panel, "V06_INSTALLER_SHA256", hashlib.sha256(legacy).hexdigest()):
            old.update(format=2, installerSHA256=panel.V06_INSTALLER_SHA256)
            self.files.atomic_bytes(self.paths.installer, legacy, 0o755)
            self.files.atomic_bytes(self.paths.config, panel.config_bytes(old), 0o644)
            self.runtime.confirm = self.base.confirm_migration
            original = self.paths.qqon.read_bytes()
            self.base.run_options(["--migrate-manager"])
            new = panel.installation_config(self.paths.config.read_bytes())
            self.assertEqual(new, panel.config3(old, hashlib.sha256(fixture.SOURCE).hexdigest()))
            self.assertEqual(self.paths.qqon.read_bytes(), original)
            self.assertEqual(self.paths.manager_v06_previous.read_bytes(), legacy)
            self.assertFalse(self.paths.consent.exists())

    def test_snapshot_rejects_unknown_owner_before_changing_identity(self):
        old = self.install()
        self.files.owner_mode(self.paths.data / "accounts.json", 1234, 1234, 0o600)
        with self.assertRaises(panel.Failure):
            self.run_privilege("--accept-system-management", "--yes")
        self.assertEqual(panel.installation_config(self.paths.config.read_bytes()), old)
        self.assertFalse(self.paths.consent.exists())
        self.assertEqual(self.files.info(self.paths.data).st_uid, 991)


if __name__ == "__main__":
    unittest.main()
