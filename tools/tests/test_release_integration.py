"""Integration checks for the publisher, installer and Go metadata reader.

Optional local fixture only: PANEL_RELEASE_INTEGRATION_GO selects an existing Go
executable to compile real static binaries. Nothing is installed or published.
"""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("release_fixture_builder", ROOT / "tools/build_release.py")
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


def installer_module():
    source = (ROOT / "scripts/install.sh").read_text(encoding="utf-8")
    body = source.split("<<'PANEL_PYTHON'\n", 1)[1].rsplit("\nPANEL_PYTHON\n", 1)[0]
    module = types.ModuleType("installer_fixture_reader")
    exec(compile(body, str(ROOT / "scripts/install.sh") + "::fixture", "exec"), module.__dict__)
    return module


class ReleaseIntegrationTests(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("PANEL_RELEASE_INTEGRATION_GO"), "optional actual-compiler release fixture; set PANEL_RELEASE_INTEGRATION_GO")
    def test_synthetic_package_is_accepted_by_installer_and_go(self):
        go = os.environ["PANEL_RELEASE_INTEGRATION_GO"]
        # Explicit test-only identity, not a real repository, commit or release.
        with tempfile.TemporaryDirectory(prefix="panel-test-only-release-") as temporary:
            output = Path(temporary) / "assets"
            manifest = builder.build_release(version="v0.4.0", repository="fixture-owner/fixture-panel",
                                             commit="a" * 40, build_time="2026-11-06T00:00:00Z",
                                             output=output, go=go)
            installer = installer_module()
            sums = installer.checksums((output / "SHA256SUMS").read_bytes())
            parsed = installer.manifest((output / "release.json").read_bytes(), sums,
                                        "fixture-owner/fixture-panel", "v0.4.0")
            self.assertEqual(parsed, manifest)
            self.assertEqual(manifest["format"], 2)
            self.assertEqual({path.name for path in output.iterdir()}, {
                "panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "install.sh", "update.sh",
                "release.json", "SHA256SUMS",
            })
            self.assertEqual(set(sums), set(builder.ASSET_NAMES) - {"SHA256SUMS"})
            for record in manifest["binaries"]:
                self.assertEqual(record["name"], "panel")
                self.assertEqual(sums[record["archive"]["name"]], record["archive"]["sha256"])
                self.assertEqual((output / record["archive"]["name"]).stat().st_size, record["archive"]["size"])
            self.assertEqual((output / "install.sh").read_bytes(), (ROOT / "scripts/install.sh").read_bytes())
            self.assertEqual((output / "update.sh").read_bytes(), installer.WRAPPER)
            env = os.environ.copy()
            env["PANEL_RELEASE_FIXTURE_DIR"] = str(output)
            subprocess.run([go, "test", "./internal/update", "-run", "^TestPublisherFixtureCompatibility$", "-count=1"],
                           cwd=ROOT, env=env, check=True, timeout=120)
            print(json.dumps({"testOnly": True, "assetCount": len(list(output.iterdir())),
                              "binarySizes": {record["arch"]: record["size"] for record in manifest["binaries"]},
                              "publisherInstallerGoCompatible": True}))


if __name__ == "__main__":
    unittest.main()
