"""Bash bootstrap tests, using only in-memory definitions and fake operations.

Run: python -I scripts/tests/test_bootstrap.py
No production script is executed; no real apt, systemctl, account lookup, Python
probe or host configuration read is reachable from the generated Bash harness.
"""
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parents[1]
SOURCE = (SCRIPTS / "install.sh").read_text(encoding="utf-8")
BASH = shutil.which("bash")


def definition(name):
    match = re.search(r"^" + re.escape(name) + r"\(\) \{\n.*?^\}\n", SOURCE, re.M | re.S)
    if match is None:
        raise AssertionError("Missing production bootstrap function: " + name)
    return match.group(0)


PURE = "\n".join(definition(name) for name in (
    "bootstrap_fail", "bootstrap_help", "bootstrap_repo", "bootstrap_version", "bootstrap_origin", "bootstrap_port", "bootstrap_menu",
    "bootstrap_arguments", "bootstrap_manage_arguments", "bootstrap_os_release", "bootstrap_missing", "bootstrap_confirm", "bootstrap"))
FAKES = r'''
PYTHON_READY=1
CA_FILE=1
CA_VALID=1
ACCOUNT_TOOLS=1
NOLOGIN=1
APT_FAIL=0
APT_NO_EFFECT=0
REPLY_VALUE=yes
REPLY_FAIL=0
APT_CALLS=0
DNF_CALLS=0
PANEL_PACKAGE_MANAGER=apt
PANEL_ACCOUNT_PACKAGE=passwd
PANEL_NOLOGIN_PACKAGE=login
MENU_ANSWERS=()
MENU_INDEX=0
bootstrap_prompt() {
    (( MENU_INDEX < ${#MENU_ANSWERS[@]} )) || bootstrap_fail 'No controlling terminal for menu.'
    REPLY=${MENU_ANSWERS[MENU_INDEX]}
    MENU_INDEX=$((MENU_INDEX + 1))
}
bootstrap_root() { printf '%s\n' root-check; }
bootstrap_system() { printf '%s\n' system-check; }
bootstrap_update_source() { printf '%s\n' source-check; }
bootstrap_python_ready() { (( PYTHON_READY )); }
bootstrap_ca_ready() { (( CA_FILE && (!$1 || CA_VALID) )); }
bootstrap_account_tools_ready() { (( ACCOUNT_TOOLS )); }
bootstrap_nologin_ready() { (( NOLOGIN )); }
bootstrap_answer() {
    printf '%s\n' tty-confirm
    (( !REPLY_FAIL )) || bootstrap_fail 'No controlling terminal; use --yes only for explicitly unattended operation.'
    REPLY=$REPLY_VALUE
}
bootstrap_apt() {
    APT_CALLS=$((APT_CALLS + 1))
    printf 'fake-apt %s\n' "$*"
    (( !APT_FAIL )) || bootstrap_fail 'Injected dependency installation failure.'
    (( !APT_NO_EFFECT )) || return 0
    local package
    for package in "$@"; do
        case $package in
            python3) PYTHON_READY=1 ;;
            ca-certificates) CA_FILE=1; CA_VALID=1 ;;
            passwd|shadow-utils) ACCOUNT_TOOLS=1 ;;
            login|util-linux-core) NOLOGIN=1 ;;
            *) bootstrap_fail 'Harness saw unapproved dependency.' ;;
        esac
    done
}
bootstrap_dnf() {
    DNF_CALLS=$((DNF_CALLS + 1))
    printf 'fake-dnf %s\n' "$*"
    bootstrap_apt "$@" >/dev/null
}
'''


@unittest.skipUnless(BASH, "Bash is required for the isolated bootstrap harness")
class BootstrapTests(unittest.TestCase):
    def run_shell(self, body, args=(), extra=""):
        # Production low-level host-reading/command-executing definitions are
        # deliberately absent, rather than relying on an environment backdoor.
        script = "set -euo pipefail\nexport LANG=C LC_ALL=C\n" + PURE + FAKES + extra + "\n" + body
        self.assertNotIn("/usr/bin/apt-get", script)
        self.assertNotIn("/usr/bin/dnf", script)
        self.assertNotIn("/usr/bin/systemctl", script)
        self.assertNotIn("/usr/bin/python3", script)
        env = {key: os.environ[key] for key in ("SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP") if key in os.environ}
        env.update(PATH="/usr/sbin:/usr/bin:/sbin:/bin", LANG="C", LC_ALL="C", HOME="/")
        return subprocess.run([BASH, "--noprofile", "--norc", "-p", "-s", "--", *args], input=script,
                              encoding="utf-8", stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              cwd=SCRIPTS, env=env, timeout=15, check=False)

    def run_bootstrap(self, setup="", args=("--repo", "synthetic-owner/synthetic-panel", "--yes"), extra=""):
        return self.run_shell(setup + '\nbootstrap "$@"\nprintf "reached-python apt-calls=%s\\n" "$APT_CALLS"\n', args, extra)

    def assert_no_apt(self, result):
        self.assertNotIn("fake-apt ", result.stdout)

    def test_help_without_python_or_supported_host_has_no_effects(self):
        for args in (("--help",), ("-h",)):
            with self.subTest(args=args):
                result = self.run_bootstrap("PYTHON_READY=0", args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("用法", result.stdout)
                for event in ("root-check", "system-check", "source-check", "fake-apt", "reached-python"):
                    self.assertNotIn(event, result.stdout)

    def test_missing_malformed_duplicate_or_override_flags_fail_before_preflight(self):
        cases = ((), ("--repo",), ("--repo", "https://github.com/a/b"), ("--repo", "a.b/repo"),
                 ("--repo", "a/-bad"), ("--repo", "a/b", "--root", "/tmp/test"), ("--update", "--repo", "a/b"),
                 ("--update", "--origin", "https://example.com"), ("--update", "--update"),
                 ("--repo=a/b", "--repo=a/b"), ("--repo=a/b", "--version=v01.2.3"),
                 ("--repo=a/b", "--version=v2147483648.0.0"), ("--repo=a/b", "--origin=https://127.0.0.1"),
                 ("--repo=a/b", "--origin=https://bad_.example.com"), ("--repo=a/b", "--origin=https://example.com:0"),
                 ("--repo=a/b", "--origin=https://example.com:65536"), ("--repo=a/b", "--origin=https://example.com/path"),
                 ("--repo=a/b", "--origin=https://example.com?"), ("--yes=true",), ("--rep=a/b",))
        for args in cases:
            with self.subTest(args=args):
                result = self.run_bootstrap("PYTHON_READY=0", args)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("root-check", result.stdout)
                self.assert_no_apt(result)

    def test_valid_cli_accepts_equal_forms_boundary_version_and_https_port(self):
        result = self.run_bootstrap(args=("--repo=A-b/R_e.po-1", "--version=v2147483647.0.42",
                                          "--origin=https://PANEL.example.com:000443/", "--yes"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("reached-python apt-calls=0", result.stdout)
        self.assert_no_apt(result)

    def test_nonroot_gate_uses_real_condition_with_synthetic_uid(self):
        root = definition("bootstrap_root").replace("EUID == 0", "1001 == 0")
        result = self.run_bootstrap("PYTHON_READY=0", extra=root)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("需要 root", result.stderr)
        self.assertNotIn("system-check", result.stdout)
        self.assert_no_apt(result)

    def test_os_release_literals_supported_only_and_never_executed(self):
        for identity in ("ID=debian\nVERSION_ID=12", 'ID="debian"\nVERSION_ID="13"',
                         "ID='ubuntu'\nVERSION_ID='22.04'", "ID=ubuntu\nVERSION_ID=24.04"):
            with self.subTest(identity=identity):
                result = self.run_shell("bootstrap_os_release <<'OS_DATA'\n" + identity + "\nOS_DATA\nprintf '%s\\n' accepted\n")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("accepted", result.stdout)
        for identity in ("ID=debian\nVERSION_ID=11", "ID=ubuntu\nVERSION_ID=20.04", "ID=alpine\nVERSION_ID=3.20",
                         "ID=debian\nID=ubuntu\nVERSION_ID=24.04", "ID=debian\nVERSION_ID=12\nVERSION_ID=13",
                         'ID="$(printf executed)"\nVERSION_ID=12', "ID=debian;printf executed\nVERSION_ID=12",
                         "ID=debian\nVERSION_ID=$release", "ID_LIKE=debian\nVERSION_ID=12"):
            with self.subTest(identity=identity):
                result = self.run_shell("bootstrap_os_release <<'OS_DATA'\n" + identity + "\nOS_DATA\n")
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("executed", result.stdout)
                self.assert_no_apt(result)

    def test_real_system_gate_reads_only_synthetic_fixtures(self):
        with tempfile.TemporaryDirectory(prefix="panel-bootstrap-host-") as directory:
            root = Path(directory)
            system = definition("bootstrap_system")
            for source, name in (("/proc/sys/kernel/ostype", "ostype"), ("/etc/os-release", "os-release"),
                                 ("/proc/1/comm", "comm"), ("/run/systemd/system", "systemd"),
                                 ("/usr/bin/systemctl", "systemctl"), ("/usr/bin/uname", "uname")):
                system = system.replace(source, '"$FIXTURE_ROOT/' + name + '"')
            system = system.replace('arch=$("$FIXTURE_ROOT/uname" -m)', 'arch=$FAKE_ARCH')
            (root / "ostype").write_bytes(b"Linux\n")
            (root / "os-release").write_bytes(b"ID=debian\nVERSION_ID=12\n")
            (root / "comm").write_bytes(b"systemd\n")
            (root / "systemd").mkdir()
            for name in ("uname", "systemctl"):
                (root / name).write_text("#!/bin/bash\nexit 99\n")
                (root / name).chmod(0o755)
            # Scripts above are only tested with -x, never executed.
            body = 'FIXTURE_ROOT=$1; shift\nFAKE_ARCH=x86_64\nPYTHON_READY=0\nbootstrap "$@"\n'
            args = (root.as_posix(), "--repo", "synthetic-owner/synthetic-panel", "--yes")
            result = self.run_shell(body, args, system)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("fake-apt python3", result.stdout)
            for name, bad in (("ostype", "Darwin\n"), ("comm", "bash\n"), ("os-release", "ID=debian\nVERSION_ID=11\n")):
                path, good = root / name, (root / name).read_bytes()
                path.write_bytes(bad.encode())
                result = self.run_shell(body, args, system)
                path.write_bytes(good)
                self.assertNotEqual(result.returncode, 0)
                self.assert_no_apt(result)
            result = self.run_shell(body.replace("FAKE_ARCH=x86_64", "FAKE_ARCH=riscv64"), args, system)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("amd64/arm64", result.stderr)
            self.assert_no_apt(result)

    def test_invalid_update_mode_stops_before_dependency_detection(self):
        result = self.run_bootstrap("PYTHON_READY=0", ("--update", "--yes"),
                                    "bootstrap_update_source() { bootstrap_fail 'Unrecognized update source'; }\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Unrecognized update source", result.stderr)
        self.assert_no_apt(result)
        source = definition("bootstrap_update_source")
        self.assertIn("/usr/local/lib/server-panel/install.sh", source)
        self.assertIn("/etc/server-panel/install.json", source)

    def test_all_dependencies_present_does_not_prompt_or_apt(self):
        result = self.run_bootstrap(args=("--repo", "synthetic-owner/synthetic-panel"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_no_apt(result)
        self.assertNotIn("tty-confirm", result.stdout)
        self.assertIn("reached-python apt-calls=0", result.stdout)

    def test_only_missing_allowlisted_packages_are_selected(self):
        for setup, expected in (("PYTHON_READY=0", "python3"), ("CA_FILE=0", "ca-certificates"),
                                ("CA_VALID=0", "ca-certificates"), ("ACCOUNT_TOOLS=0", "passwd"),
                                ("NOLOGIN=0", "login"),
                                ("PYTHON_READY=0; CA_FILE=0; ACCOUNT_TOOLS=0; NOLOGIN=0", "python3 ca-certificates passwd login")):
            with self.subTest(setup=setup):
                result = self.run_bootstrap(setup)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("fake-apt " + expected + "\n", result.stdout)
                self.assertIn("reached-python apt-calls=1", result.stdout)
                self.assertNotIn("tty-confirm", result.stdout)

    def test_tty_confirmation_precedes_apt_and_cancellation_leaves_host_untouched(self):
        args = ("--repo", "synthetic-owner/synthetic-panel")
        result = self.run_bootstrap("PYTHON_READY=0", args)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLess(result.stdout.index("tty-confirm"), result.stdout.index("fake-apt"))
        for answer in ("no", "YES", "yesplease"):
            with self.subTest(answer=answer):
                result = self.run_bootstrap("PYTHON_READY=0; REPLY_VALUE=" + answer, args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("已取消", result.stderr)
                self.assert_no_apt(result)
        result = self.run_bootstrap("PYTHON_READY=0; REPLY_FAIL=1", args)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("没有控制终端", result.stderr)
        self.assert_no_apt(result)

    def test_failed_apt_or_postcheck_never_reaches_python_management(self):
        for setup, message in (("PYTHON_READY=0; APT_FAIL=1", "Injected dependency installation failure"),
                               ("PYTHON_READY=0; APT_NO_EFFECT=1", "依赖安装后仍不可用")):
            with self.subTest(setup=setup):
                result = self.run_bootstrap(setup)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertNotIn("reached-python", result.stdout)
                self.assertEqual(result.stdout.count("fake-apt "), 1)

    def test_ca_content_is_rechecked_after_installing_absent_python(self):
        result = self.run_bootstrap("PYTHON_READY=0; CA_VALID=0")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("fake-apt python3\n", result.stdout)
        self.assertIn("fake-apt ca-certificates\n", result.stdout)
        self.assertIn("reached-python apt-calls=2", result.stdout)

    def test_apt_commands_have_fixed_paths_clean_env_and_finite_bounds(self):
        apt = definition("bootstrap_apt")
        self.assertEqual(apt.count("/usr/bin/apt-get"), 3)  # preflight + two operations
        self.assertEqual(apt.count("/usr/bin/env -i"), 2)
        self.assertEqual(apt.count("DEBIAN_FRONTEND=noninteractive"), 2)
        self.assertEqual(apt.count("--kill-after=30s"), 2)
        for required in ("DPkg::Lock::Timeout=30", "Acquire::Retries=2", "Acquire::https::Timeout=15", "180s", "300s",
                         'install --yes --no-install-recommends "$@"', "python3|ca-certificates|passwd|login"):
            self.assertIn(required, apt)
        for forbidden in ("apt upgrade", "dist-upgrade", "full-upgrade", "sources.list", "curl", "eval ", "source "):
            self.assertNotIn(forbidden, apt)

    def test_menu_install_port_proxy_reprompts_and_forwards_arguments(self):
        setup = 'MENU_ANSWERS=(9 1 9 3 80 6217 http://bad https://panel.example.com)\n'
        result = self.run_shell(setup + 'bootstrap\nprintf "arg=<%s>\\n" "${PANEL_ARGS[@]}"\n')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("arg=<--port>\narg=<6217>", result.stdout)
        self.assertIn("arg=<--origin>\narg=<https://panel.example.com>", result.stdout)
        self.assertNotIn("arg=<--yes>", result.stdout)
        self.assert_no_apt(result)

    def test_menu_exit_upgrade_status_and_default_local_mode(self):
        for answers, expected in (("0", None), ("2", "--upgrade"), ("3", "--status"), ('1 2 ""', "5173"), ('1 "" "" 8.8.8.8', "8443")):
            with self.subTest(answers=answers):
                result = self.run_shell('MENU_ANSWERS=(' + answers + ')\nbootstrap\nprintf "arg=<%s>\\n" "${PANEL_ARGS[@]}"\n')
                self.assertEqual(result.returncode, 0, result.stderr)
                if expected is None:
                    self.assertNotIn("root-check", result.stdout)
                else:
                    self.assertIn("arg=<" + expected + ">", result.stdout)
                if expected == "--status":
                    self.assertNotIn("system-check", result.stdout)
                self.assert_no_apt(result)

    def test_status_never_bootstraps_packages_or_requires_systemd(self):
        for ready in (0, 1):
            result = self.run_bootstrap("PYTHON_READY=" + str(ready) + "; CA_FILE=0; ACCOUNT_TOOLS=0", ("--status",))
            self.assertEqual(result.returncode, 0 if ready else 1)
            for event in ("system-check", "source-check", "fake-apt", "fake-dnf", "tty-confirm"):
                self.assertNotIn(event, result.stdout)
        for args in (("--status", "--yes"), ("--status", "--version=v0.5.0"), ("--upgrade", "--port=6217"),
                     ("--status", "--repo=a/b"), ("--upgrade", "--update"), ("--migrate-manager", "--upgrade"),
                     ("--port=80",), ("--port=65536",), ("--port=06217",)):
            with self.subTest(args=args):
                result = self.run_bootstrap(args=args)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("root-check", result.stdout)

    def test_dnf_mapping_missing_packages_confirmation_and_failure(self):
        for identity in ("rocky:9", "rocky:9.5", "rocky:10", "almalinux:9", "almalinux:10.1"):
            os_id, version = identity.split(":")
            setup = "bootstrap_os_release <<'OS_DATA'\nID=" + os_id + "\nVERSION_ID=" + version + "\nOS_DATA\n"
            setup += "PYTHON_READY=0; CA_FILE=0; ACCOUNT_TOOLS=0; NOLOGIN=0\n"
            result = self.run_bootstrap(setup)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("fake-dnf python3 ca-certificates shadow-utils util-linux-core", result.stdout)
            self.assert_no_apt(result)
        result = self.run_bootstrap(setup + "APT_FAIL=1")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("reached-python", result.stdout)
        for bad in ("8", "11", "9.2evil", "10.1.2"):
            result = self.run_shell("bootstrap_os_release <<'OS_DATA'\nID=rocky\nVERSION_ID=" + bad + "\nOS_DATA\n")
            self.assertNotEqual(result.returncode, 0)

    def test_dnf_is_fixed_allowlist_bounded_and_no_security_policy_changes(self):
        dnf = definition("bootstrap_dnf")
        for required in ("python3|ca-certificates|shadow-utils|util-linux-core", "/usr/bin/env -i", "/usr/bin/dnf",
                         "--kill-after=30s 300s", "--setopt=timeout=15", "--setopt=retries=2", "--setopt=exit_on_lock=True",
                         "--setopt=install_weak_deps=False", 'install --assumeyes "$@"'):
            self.assertIn(required, dnf)
        for forbidden in ("upgrade", "setenforce", "semanage", "permissive", "--nogpgcheck", "repo=", "eval "):
            self.assertNotIn(forbidden, dnf)

    def test_v06_maintenance_readonly_and_fixed_argument_gates(self):
        for args in (("--manage",), ("--manage", "status"), ("--manage", "url"), ("--manage", "logs", "--lines", "200")):
            result = self.run_bootstrap("CA_FILE=0; ACCOUNT_TOOLS=0", args)
            self.assertEqual(result.returncode, 0, result.stderr)
            for event in ("root-check", "system-check", "source-check", "fake-apt", "fake-dnf"):
                self.assertNotIn(event, result.stdout)
        for args in (("--manage", "start", "--yes"), ("--manage", "tls", "renew", "--yes"), ("--manage", "backup", "--yes")):
            result = self.run_bootstrap("CA_FILE=0; ACCOUNT_TOOLS=0", args)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("root-check", result.stdout)
            self.assert_no_apt(result)
        for args in (("--manage", "status", "--yes"), ("--manage", "start", "--unit", "other.service"),
                     ("--manage", "logs", "--lines", "201"), ("--manage", "tls", "inspect"), ("--manage", "url", "--port", "8443")):
            result = self.run_bootstrap(args=args)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("root-check", result.stdout)
            self.assert_no_apt(result)

    def test_dependency_probes_use_only_system_python_fixed_ca_and_used_tools(self):
        python = definition("bootstrap_python_ready")
        ca = definition("bootstrap_ca_ready")
        self.assertIn("/usr/bin/python3 -I -c", python)
        self.assertIn("sys.version_info >= (3, 8)", python)
        self.assertIn('"$PANEL_CA_FILE"', ca)
        self.assertIn("/etc/ssl/certs/ca-certificates.crt", definition("bootstrap_os_release"))
        self.assertIn("/etc/pki/tls/certs/ca-bundle.crt", definition("bootstrap_os_release"))
        self.assertIn('cert_store_stats()["x509_ca"]', ca)
        self.assertIn("/usr/sbin/useradd", definition("bootstrap_account_tools_ready"))
        self.assertIn("/usr/sbin/groupadd", definition("bootstrap_account_tools_ready"))
        self.assertIn("/usr/sbin/nologin", definition("bootstrap_nologin_ready"))
        answer = definition("bootstrap_answer")
        self.assertIn("> /dev/tty", answer)
        self.assertIn("< /dev/tty", answer)
        self.assertNotIn("<> /dev/tty", answer)


if __name__ == "__main__":
    unittest.main(verbosity=2)
