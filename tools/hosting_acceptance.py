"""Opt-in real hosting acceptance, exclusively on a disposable GitHub Ubuntu runner.

This is not an installer and refuses developer machines and self-hosted runners.
It starts the checked-out build, uses its authenticated API, and reports only
booleans/versions/timings. Credentials and uploaded payloads never enter the report.
"""
import argparse
import contextlib
import datetime
import hashlib
import http.client
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import statistics
import subprocess
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


MAX_RESPONSE = 4 << 20


def guard():
    required = {"QQON_REAL_HOST_TEST": "ephemeral-ubuntu-24.04", "GITHUB_ACTIONS": "true",
                "RUNNER_ENVIRONMENT": "github-hosted", "RUNNER_OS": "Linux"}
    if any(os.environ.get(key) != value for key, value in required.items()) or os.geteuid() != 0:
        raise SystemExit("Real hosting test requires explicit opt-in on a GitHub-hosted Linux runner as root")
    release = Path("/etc/os-release").read_text(encoding="utf-8")
    if 'VERSION_ID="24.04"' not in release or "Ubuntu" not in release or not Path("/run/systemd/system").is_dir():
        raise SystemExit("Real hosting test requires disposable Ubuntu 24.04 with active systemd")
    for path in ("/etc/server-panel", "/var/lib/qqon-hosting-ci", "/srv/qqon-sites"):
        if os.path.lexists(path):
            raise SystemExit("Acceptance refuses existing panel/site state: " + path)


def command(arguments, timeout=60):
    result = subprocess.run(arguments, check=True, capture_output=True, timeout=timeout, text=True, encoding="utf-8")
    return result.stdout


def port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


class Acceptance:
    def __init__(self, binary):
        self.binary = binary
        self.directory = Path("/var/lib/qqon-hosting-ci")
        self.directory.mkdir(mode=0o700)
        self.listen_port = port()
        self.base = "http://127.0.0.1:" + str(self.listen_port)
        self.jar = http.cookiejar.CookieJar()
        self.client = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(self.jar))
        self.csrf = ""
        self.password = secrets.token_urlsafe(30)
        self.process = None
        self.log = (self.directory / "acceptance-runtime.log").open("xb")
        self.report = {"environment": "GitHub-hosted Ubuntu 24.04 ephemeral runner", "checks": {}}

    def start(self):
        self.process = subprocess.Popen([self.binary, "-listen", "127.0.0.1:" + str(self.listen_port),
                                         "-data-dir", str(self.directory)], stdin=subprocess.DEVNULL,
                                        stdout=self.log, stderr=self.log, cwd="/", start_new_session=True,
                                        env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LANG": "C.UTF-8", "HOME": "/root"})
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise AssertionError("Acceptance panel exited during startup")
            try:
                if self.request("GET", "/healthz")[0] == 200:
                    return
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.2)
        raise AssertionError("Panel health startup timeout")

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.send_signal(signal.SIGTERM)
            try:
                self.process.wait(timeout=60)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
                raise AssertionError("Panel did not finish bounded graceful shutdown")
        self.process = None

    def request(self, method, path, body=None, csrf=True, headers=None, anonymous=False, raw=False):
        data = body if isinstance(body, bytes) else (json.dumps(body).encode() if body is not None else None)
        request_headers = {"Origin": self.base}
        if data is not None:
            request_headers["Content-Type"] = "application/octet-stream" if raw else "application/json"
        if csrf and self.csrf:
            request_headers["X-CSRF-Token"] = self.csrf
        request_headers.update(headers or {})
        req = urllib.request.Request(self.base + path, data=data, method=method, headers=request_headers)
        client = self.client if not anonymous else urllib.request.build_opener(urllib.request.ProxyHandler({}))
        try:
            response = client.open(req, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read(MAX_RESPONSE + 1)
            assert len(data) <= MAX_RESPONSE, "API response exceeded bound"
            return response.status, json.loads(data)

    def expect(self, method, path, body=None, code=200, **kwargs):
        actual, value = self.request(method, path, body, **kwargs)
        assert actual == code, f"{method} {path} returned {actual}: {value.get('error', {})}"
        return value

    def login(self, initialize=False):
        body = {"username": "acceptance-admin", "password": self.password}
        if initialize:
            body["token"] = (self.directory / "setup-token").read_text().strip()
        result = self.expect("POST", "/api/auth/" + ("setup" if initialize else "login"), body)
        assert result["authenticated"]
        self.csrf = result["csrfToken"]

    def wait(self, result, success=True):
        job = result["job"]
        deadline = time.monotonic() + 14 * 60
        while job["status"] in ("queued", "running"):
            assert time.monotonic() < deadline, "Job exceeded acceptance deadline"
            time.sleep(0.2)
            job = self.expect("GET", "/api/jobs/" + job["id"])["job"]
        expected = "succeeded" if success else "failed"
        assert job["status"] == expected, f"{job['action']} ended {job['status']}: {job.get('error', '')}; output {job.get('logs', [])[-8:]}"
        return job

    def operation(self, method, path, body, success=True, **kwargs):
        return self.wait(self.expect(method, path, body, code=202, **kwargs), success)

    def host(self, site, path="/"):
        connection = http.client.HTTPConnection("127.0.0.1", site["port"], timeout=5)
        try:
            connection.request("GET", path, headers={"Host": site["domains"][0], "Connection": "close"})
            response = connection.getresponse()
            data = response.read((2 << 20) + 1)
            assert len(data) <= 2 << 20
            return response.status, data
        finally:
            connection.close()

    def site(self, name, kind="static", **extra):
        input_site = {"name": name, "type": kind, "domains": [name + ".example.test"], "port": port(), "enabled": True, **extra}
        job = self.operation("POST", "/api/sites", {"confirmed": True, "site": input_site})
        result = self.expect("GET", "/api/sites/" + job["target"])["site"]
        assert result["name"] == name and result["enabled"]
        return result

    def patch_site(self, site, **changes):
        fields = ("name", "type", "domains", "port", "enabled", "upstream", "phpRuntime")
        input_site = {key: site[key] for key in fields if key in site}
        input_site.update(changes)
        self.operation("PATCH", "/api/sites/" + site["id"], {"confirmed": True, "revision": site["revision"], "site": input_site})
        return self.expect("GET", "/api/sites/" + site["id"])["site"]

    def run(self):
        self.start()
        self.expect("GET", "/api/apps", code=401, anonymous=True)
        self.login(initialize=True)
        before = self.expect("GET", "/api/management")
        assert not before["authorized"], "Root alone implicitly authorized management"
        self.expect("POST", "/api/apps/nginx/install", {"confirmed": True}, code=403)
        self.report["checks"]["rootWithoutConsentRejected"] = True
        consent_dir = Path("/etc/server-panel")
        consent_dir.mkdir(mode=0o755)
        record = {"format": 1, "mode": "system", "identity": "root", "noticeVersion": 1,
                  "acceptedAt": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                  "acceptedBy": "root", "notice": "full-system-management"}
        # Only this explicitly opted-in ephemeral test writes a synthetic consent;
        # ordinary application/installer tests have no production path override.
        consent_file = consent_dir / "management-consent.json"
        consent_file.write_text(json.dumps(record), encoding="utf-8")
        consent_file.chmod(0o644)
        assert self.expect("GET", "/api/management")["authorized"]
        self.stop()
        self.start()
        self.login()
        self.expect("POST", "/api/apps/nginx/install", {"confirmed": True}, csrf=False, code=403)
        self.expect("POST", "/api/apps/nginx/install", {"confirmed": True}, headers={"Origin": "https://untrusted.example"}, code=403)
        self.report["checks"]["csrfAndCrossOriginRejected"] = True
        initial = self.expect("GET", "/api/apps")
        self.report["initialPackages"] = [{"id": item["id"], "installed": item["installed"]} for item in initial["items"]]
        assert not next(item for item in initial["items"] if item["id"] == "nginx")["installed"], "CI runner should start without nginx"
        for application in ("nginx", "php-fpm"):
            self.operation("POST", "/api/apps/" + application + "/install", {"confirmed": True})
            self.operation("POST", "/api/apps/" + application + "/start", {"confirmed": True})
        installed = self.expect("GET", "/api/apps")
        self.report["installedPackages"] = [{key: item[key] for key in ("id", "installed", "version", "state")} for item in installed["items"]]
        assert installed["phpRuntimes"], "No real active PHP-FPM runtime detected"
        self.report["checks"]["realNginxPHPInstallAndStart"] = True
        site = self.site("qqon-ci-static")
        assert self.host(site)[0] == 200 and site["id"].encode() in self.host(site)[1]
        code, text = self.host(site, "/.__qqon_health")
        assert code == 200 and site["id"].encode() in text
        connection = http.client.HTTPConnection("127.0.0.1", site["port"], timeout=5)
        connection.request("GET", "/", headers={"Host": "127.0.0.1"})
        assert connection.getresponse().status == 421
        connection.close()
        self.report["checks"]["staticHTTPAndPanelHostIsolation"] = True
        self.operation("POST", "/api/sites/" + site["id"] + "/upload?path=hello.txt", b"real uploaded website content", raw=True, headers={"X-Confirm-Operation": "true"})
        assert self.host(site, "/hello.txt") == (200, b"real uploaded website content")
        content = self.expect("GET", "/api/sites/" + site["id"] + "/content?path=hello.txt")
        self.operation("PUT", "/api/sites/" + site["id"] + "/content", {"confirmed": True, "path": "hello.txt", "content": "updated through bounded editor", "sha256": content["sha256"]})
        assert self.host(site, "/hello.txt") == (200, b"updated through bounded editor")
        self.report["checks"]["realUploadEditHTTP"] = True
        site = self.patch_site(site, enabled=False)
        assert self.host(site)[0] == 503
        site = self.patch_site(site, enabled=True)
        assert self.host(site)[0] == 200
        self.report["checks"]["disableEnableHTTP"] = True

        class Upstream(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.send_header("Content-Type", "text/plain")
                self.end_headers()
                self.wfile.write(b"actual reverse proxy upstream")
            def log_message(self, *args):
                pass

        upstream = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        threading.Thread(target=upstream.serve_forever, daemon=True).start()
        try:
            proxy = self.site("qqon-ci-proxy", "proxy", upstream="http://127.0.0.1:" + str(upstream.server_port))
            assert self.host(proxy) == (200, b"actual reverse proxy upstream")
            self.report["checks"]["reverseProxyRealUpstream"] = True
            php = self.site("qqon-ci-php", "php", phpRuntime=installed["phpRuntimes"][0]["id"])
            php_code, php_body = self.host(php, "/index.php")
            assert php_code == 200 and php_body == ("QQON PHP " + php["id"]).encode() and b"<?php" not in php_body
            self.report["checks"]["phpFPMExecutedNotSourceServed"] = True
            # Force a real nginx -t failure using one explicitly test-owned file;
            # managed config and revision must roll back without changing it.
            invalid = Path("/etc/nginx/conf.d/qqon-ci-invalid-fixture.conf")
            assert not os.path.lexists(invalid)
            owned = Path("/etc/nginx/conf.d/qqon-" + site["id"] + ".conf")
            original = owned.read_bytes()
            invalid.write_text("deliberately_invalid_qqon_test_directive;\n", encoding="ascii")
            invalid.chmod(0o644)
            try:
                input_site = {key: site[key] for key in ("name", "type", "domains", "port", "enabled")}
                input_site["name"] = "should-not-commit"
                self.operation("PATCH", "/api/sites/" + site["id"], {"confirmed": True, "revision": site["revision"], "site": input_site}, success=False)
                assert owned.read_bytes() == original, "Failed nginx test did not restore owned configuration"
                assert invalid.read_text() == "deliberately_invalid_qqon_test_directive;\n", "Unknown configuration overwritten"
            finally:
                invalid.unlink()
            self.stop()
            self.start()
            self.login()
            site = self.expect("GET", "/api/sites/" + site["id"])["site"]
            assert site["name"] == "qqon-ci-static" and self.host(site)[0] == 200
            assert self.host(proxy)[0] == 200 and self.host(php, "/index.php")[0] == 200
            self.report["checks"]["nginxFailureRollbackAndRestartRecovery"] = True
            for owned_site in (site, proxy, php):
                self.operation("DELETE", "/api/sites/" + owned_site["id"], {"confirmed": True, "revision": owned_site["revision"]})
                assert Path(owned_site["root"]).is_dir(), "Delete removed website files"
                assert not Path("/etc/nginx/conf.d/qqon-" + owned_site["id"] + ".conf").exists()
            self.report["checks"]["deletePreservesWebsiteFiles"] = True
        finally:
            upstream.shutdown()
            upstream.server_close()
        samples = []
        for _ in range(5):
            time.sleep(1)
            status = Path("/proc", str(self.process.pid), "status").read_text()
            samples.append(int(next(line.split()[1] for line in status.splitlines() if line.startswith("VmRSS:"))))
        self.report["panelRSSMiB"] = {"samples": len(samples), "median": round(statistics.median(samples) / 1024, 2), "max": round(max(samples) / 1024, 2),
                                      "scope": "post-operations short panel process sampling, not service memory or long soak"}
        self.report["checks"]["restartPreservedAccountsAndTasks"] = len(self.expect("GET", "/api/jobs")["jobs"]) > 0
        consent_file.unlink()
        assert not self.expect("GET", "/api/management")["authorized"]
        self.expect("POST", "/api/apps/nginx/restart", {"confirmed": True}, code=403)
        self.report["checks"]["consentRevocationImmediatelyBlocksWrites"] = True
        return self.report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--report", required=True)
    args = parser.parse_args()
    guard()
    binary = str(Path(args.binary).resolve(strict=True))
    test = Acceptance(binary)
    try:
        report = test.run()
        output = Path(args.report)
        assert not output.exists()
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(report, ensure_ascii=False, indent=2))
    finally:
        test.stop()
        test.log.close()


if __name__ == "__main__":
    main()
