"""Offline checks for the Pages shell entry; no download, sudo or root writes."""

from pathlib import Path
import re
import shutil
import subprocess
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "bootstrap.sh"


class EntryTests(unittest.TestCase):
    def run_bash(self, script):
        bash = shutil.which("bash")
        if not bash:
            self.skipTest("Bash unavailable")
        return subprocess.run([bash, "-c", script], capture_output=True, text=True, timeout=5)

    def test_syntax_and_help_have_no_host_effects(self):
        bash = shutil.which("bash")
        if not bash:
            self.skipTest("Bash unavailable")
        text = SOURCE.read_text(encoding="utf-8")
        syntax = subprocess.run([bash, "-n"], input=text, capture_output=True, text=True, timeout=5)
        self.assertEqual(syntax.returncode, 0, syntax.stderr)
        help_result = subprocess.run([bash, "-s", "--", "--help"], input=text, capture_output=True, text=True, timeout=5)
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertIn("interactive installer", help_result.stdout)

    def test_redirects_are_limited_to_repository_and_asset_hosts(self):
        text = SOURCE.read_text(encoding="utf-8")
        definition = text.split("allowed_url() {", 1)[1].split("\n}\n", 1)[0]
        function = "allowed_url() {" + definition + "\n}\n"
        accepted = ["https://github.com/LinyuFiee/QQON-Panel/releases/latest/download/install.sh",
                    "https://github.com/LinyuFiee/QQON-Panel/releases/download/v0.5.0/install.sh",
                    "https://release-assets.githubusercontent.com/asset?signed=true"]
        rejected = ["http://github.com/LinyuFiee/QQON-Panel/releases/download/v0.5.0/install.sh",
                    "https://github.com.evil.example/x", "https://127.0.0.1/install.sh",
                    "https://user:pass@github.com/LinyuFiee/QQON-Panel/releases/download/v0.5.0/install.sh",
                    "https://github.com/other/repo/releases/download/v0.5.0/install.sh",
                    "https://objects.githubusercontent.com:444/a"]
        for address in accepted + rejected:
            with self.subTest(url=address):
                # Values are fixed test constants, not external shell input.
                result = self.run_bash(function + "allowed_url '" + address + "'")
                self.assertEqual(result.returncode == 0, address in accepted)

    def test_downloaded_script_is_verified_before_execution(self):
        text = SOURCE.read_text(encoding="utf-8")
        self.assertLess(text.index('actual=$(sha256sum'), text.index('/bin/bash -p "$stage/install.sh"'))
        self.assertIn("interactive installer protocol: 3", text)
        self.assertIn("umask 077", text)
        self.assertIn("mktemp -d /root/", text)
        self.assertIn("--max-redirs 0", text)
        self.assertIn("ulimit -f", text)
        self.assertNotRegex(text, r"(?:--insecure|\bcurl\s+-k\b|--no-check-certificate)")


if __name__ == "__main__":
    unittest.main()
