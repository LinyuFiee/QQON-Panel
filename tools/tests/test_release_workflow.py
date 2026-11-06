"""Offline structural/syntax guards for the release workflow, not a CI run."""

from pathlib import Path
import re
import shutil
import subprocess
import textwrap
import unittest


WORKFLOW = Path(__file__).resolve().parents[2] / ".github" / "workflows" / "release.yml"


class WorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = WORKFLOW.read_text(encoding="utf-8")
        lines = cls.text.splitlines()
        cls.scripts = []
        for index, line in enumerate(lines):
            if line.strip() != "run: |":
                continue
            indent = len(line) - len(line.lstrip())
            block = []
            for following in lines[index + 1:]:
                if following.strip() and len(following) - len(following.lstrip()) <= indent:
                    break
                block.append(following)
            cls.scripts.append(textwrap.dedent("\n".join(block)))

    def test_official_actions_are_verified_immutable_pins(self):
        expected = {
            "actions/checkout": "11bd71901bbe5b1630ceea73d27597364c9af683",
            "actions/setup-go": "d35c59abb061a4a6fb18e82ac0862c26744d6ab5",
            "actions/upload-artifact": "ea165f8d65b6e75b540449e92b4886f43607fa02",
            "actions/download-artifact": "d3f86a106a0bac45b974a628896c90dbdf5c8093",
        }
        actions = re.findall(r"uses: ([^\s@]+)@([^\s#]+)", self.text)
        self.assertEqual({name for name, _ in actions}, set(expected))
        for name, revision in actions:
            self.assertEqual(revision, expected[name])

    def test_permissions_tests_and_draft_publication_boundaries(self):
        self.assertIn("permissions: {}", self.text)
        test_build, publish = self.text.split("\n  publish:\n", 1)
        self.assertIn("contents: read", test_build)
        self.assertNotIn("contents: write", test_build)
        self.assertNotIn("GH_TOKEN", test_build)
        self.assertIn("contents: write", publish)
        self.assertEqual(self.text.count("GH_TOKEN:"), 1)
        for command in ("go test ./...", "go vet ./...", "go test -race ./...",
                        "python3 -I -m unittest discover -s tools/tests",
                        "python3 -I -m unittest discover -s scripts/tests"):
            self.assertIn(command, test_build)
        self.assertLess(test_build.index("go test -race ./..."), test_build.index("--output"))
        for script in self.scripts:
            self.assertNotIn("${{", script, "expressions belong in env, not shell source")
        self.assertLess(publish.index("--verify-only"), publish.index("gh release create"))
        self.assertLess(publish.index("gh release create"), publish.index("gh release upload"))
        self.assertLess(publish.index("gh release upload"), publish.index("gh release edit"))
        self.assertIn("--verify-tag", publish)
        self.assertIn("--draft", publish)
        self.assertNotIn("--clobber", publish)

    def test_exact_six_compressed_assets_and_safe_local_build_info(self):
        test_build, publish = self.text.split("\n  publish:\n", 1)
        names = {"panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "install.sh", "update.sh",
                 "release.json", "SHA256SUMS"}
        uploaded = re.findall(r"^            \$\{\{ runner.temp \}\}/[^\n]+/([^/\n]+)$", test_build, re.MULTILINE)
        self.assertEqual(len(uploaded), 6)
        self.assertEqual(set(uploaded), names)
        self.assertEqual(set(re.findall(r'"\$assets/([^"\n]+)"', publish)), names)
        self.assertNotRegex(self.text, r"panel-linux-(?:amd64|arm64)(?![\w.-])")
        self.assertIn("release.verify_release(", test_build)
        self.assertIn("release.extract_archive(", test_build)
        self.assertIn("tempfile.TemporaryDirectory(", test_build)
        self.assertLess(test_build.index("release.verify_release("), test_build.index("release.extract_archive("))
        self.assertLess(test_build.index("release.extract_archive("), test_build.index('[str(binary), "-build-info"]'))
        self.assertNotIn('"-build-info"', publish)
        self.assertNotIn("extract_archive(", publish)
        self.assertNotIn("extractall(", self.text)
        self.assertNotRegex(self.text, r"^\s+tar\s", "no unrestricted tar extraction")
        self.assertIn("--verify-only", publish)

    def test_embedded_python_compiles(self):
        snippets = 0
        for script in self.scripts:
            for match in re.finditer(r"^[^\n]*python3 -I [^\n]*<<'PY'\n(.*?)^PY$", script, re.MULTILINE | re.DOTALL):
                compile(match.group(1), str(WORKFLOW), "exec")
                snippets += 1
        self.assertEqual(snippets, 4)

    def test_embedded_shell_parses_without_executing(self):
        bash = shutil.which("bash")
        if not bash:
            self.skipTest("Bash unavailable; Linux Actions runs this guard")
        self.assertTrue(self.scripts)
        for index, script in enumerate(self.scripts):
            result = subprocess.run([bash, "-n"], input=script, text=True, capture_output=True, timeout=15)
            with self.subTest(block=index):
                self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
