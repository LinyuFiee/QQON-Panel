#!/bin/bash
# Review this local file before running it as root. No remote script is executed.
set -euo pipefail
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
    printf '%s\n' 'Execute install.sh; do not source it.' >&2
    return 1
fi
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
export LANG=C LC_ALL=C
unset BASH_ENV ENV PYTHONPATH PYTHONHOME SSL_CERT_FILE SSL_CERT_DIR APT_CONFIG

bootstrap_fail() {
    printf 'panel installer: %s\n' "$*" >&2
    exit 1
}

bootstrap_help() {
    printf '%s\n' \
        'Usage: sudo bash install.sh --repo OWNER/REPO [--version v0.4.0] [--origin https://panel.example.com] [--yes]' \
        '       sudo panel-update [--version v0.4.0] [--yes]' \
        '' \
        'Requires root and running systemd on Debian 12/13 or Ubuntu 22.04/24.04 (amd64/arm64).' \
        'Downloads format 2 tar.gz releases; verifies archive and inner panel size/SHA-256.' \
        'Only missing Python 3.8+, CA certificates and used account utilities are installed with APT.' \
        'APT changes require --yes or confirmation through /dev/tty; no system upgrade or source changes.' \
        'The panel runs as server-panel, never root. Updates preserve private data and recovery markers.' \
        '--help, -h  Show this help without changing the host.'
}

bootstrap_repo() {
    [[ $1 =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,98}[A-Za-z0-9])?/[A-Za-z0-9]([A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?$ ]] ||
        bootstrap_fail 'Use a valid public GitHub OWNER/REPO, not a URL.'
}

bootstrap_version() {
    [[ $1 =~ ^v(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})$ ]] ||
        bootstrap_fail 'Only stable vMAJOR.MINOR.PATCH releases are supported.'
    (( 10#${BASH_REMATCH[1]} <= 2147483647 && 10#${BASH_REMATCH[2]} <= 2147483647 && 10#${BASH_REMATCH[3]} <= 2147483647 )) ||
        bootstrap_fail 'Version component is too large.'
}

bootstrap_origin() {
    [[ -n $1 ]] || return 0
    [[ ${#1} -le 300 && $1 =~ ^https://([A-Za-z0-9.-]+)(:([0-9]+))?/?$ ]] ||
        bootstrap_fail 'Origin must be an HTTPS domain without credentials, path, query or fragment.'
    local host=${BASH_REMATCH[1]} port=${BASH_REMATCH[3]-} label rest
    [[ ${#host} -le 253 && $host == *.* && $host != *. && ! $host =~ ^[0-9.]+$ ]] ||
        bootstrap_fail 'Origin requires a domain name, not an IP address or localhost.'
    rest=$host
    while :; do
        label=${rest%%.*}
        [[ $label =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]] || bootstrap_fail 'Invalid origin domain.'
        [[ $rest == *.* ]] || break
        rest=${rest#*.}
    done
    if [[ -n $port ]]; then
        while [[ ${#port} -gt 1 && $port == 0* ]]; do port=${port#0}; done
        [[ ${#port} -le 5 ]] && (( 10#$port >= 1 && 10#$port <= 65535 )) || bootstrap_fail 'Invalid origin port.'
    fi
}

bootstrap_arguments() {
    PANEL_BOOTSTRAP_YES=0
    PANEL_BOOTSTRAP_UPDATE=0
    local option name value seen=' ' has_repo=0 has_origin=0
    while (( $# )); do
        option=$1
        shift
        case $option in
            --help|-h) bootstrap_help; exit 0 ;;
            --yes|--update) name=$option; value='' ;;
            --repo|--version|--origin)
                name=$option
                (( $# )) || bootstrap_fail "Missing value for $name."
                value=$1
                shift ;;
            --repo=*|--version=*|--origin=*) name=${option%%=*}; value=${option#*=} ;;
            *) bootstrap_fail "Unknown command-line option: $option" ;;
        esac
        [[ $seen != *" $name "* ]] || bootstrap_fail 'Duplicate command-line option.'
        seen+="$name "
        case $name in
            --yes) PANEL_BOOTSTRAP_YES=1 ;;
            --update) PANEL_BOOTSTRAP_UPDATE=1 ;;
            --repo) bootstrap_repo "$value"; has_repo=1 ;;
            --version) bootstrap_version "$value" ;;
            --origin) bootstrap_origin "$value"; has_origin=1 ;;
        esac
    done
    if (( PANEL_BOOTSTRAP_UPDATE )); then
        (( !has_repo && !has_origin )) || bootstrap_fail 'Update settings come only from root-owned install.json; --repo/--origin are forbidden.'
    else
        (( has_repo )) || bootstrap_fail 'First installation requires --repo OWNER/REPO.'
    fi
}

bootstrap_root() {
    (( EUID == 0 )) || bootstrap_fail 'Management requires root; no packages or services were changed.'
}

bootstrap_os_release() {
    # os-release is DATA, never shell code. Only literal ID and VERSION_ID values
    # matter; shell escapes, expansions and duplicate fields are not accepted.
    local line key value id='' version='' seen=' '
    while IFS= read -r line || [[ -n $line ]]; do
        case $line in
            ID=*|VERSION_ID=*)
                key=${line%%=*}; value=${line#*=}
                [[ $seen != *" $key "* ]] || bootstrap_fail 'Duplicate operating-system identity field.'
                seen+="$key "
                case $value in
                    \"*\") value=${value:1:${#value}-2} ;;
                    \'*\') value=${value:1:${#value}-2} ;;
                esac
                if [[ $key == ID ]]; then id=$value; else version=$value; fi ;;
        esac
    done
    case $id:$version in
        debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;;
        *) bootstrap_fail 'Supported systems: Debian 12/13 or Ubuntu 22.04/24.04 only.' ;;
    esac
}

bootstrap_system() {
    local kernel='' init='' arch=''
    [[ -r /proc/sys/kernel/ostype ]] && IFS= read -r kernel < /proc/sys/kernel/ostype ||
        bootstrap_fail 'Management requires Linux.'
    [[ $kernel == Linux ]] || bootstrap_fail 'Management requires Linux.'
    [[ -r /etc/os-release ]] || bootstrap_fail 'Cannot read operating-system identity.'
    bootstrap_os_release < /etc/os-release
    [[ -r /proc/1/comm ]] && IFS= read -r init < /proc/1/comm || bootstrap_fail 'Running systemd is required.'
    [[ $init == systemd && -d /run/systemd/system && ! -L /run/systemd/system && -x /usr/bin/systemctl ]] ||
        bootstrap_fail 'Running systemd is required; systemd will not be installed into a container.'
    [[ -x /usr/bin/uname ]] || bootstrap_fail 'The supported base system must provide uname.'
    arch=$(/usr/bin/uname -m)
    case $arch in x86_64|aarch64) ;; *) bootstrap_fail 'Only Linux amd64/arm64 are supported.' ;; esac
}

bootstrap_update_source() {
    if (( PANEL_BOOTSTRAP_UPDATE )); then
        [[ ${BASH_SOURCE[0]} == /usr/local/lib/server-panel/install.sh && -f /etc/server-panel/install.json && ! -L /etc/server-panel/install.json ]] ||
            bootstrap_fail 'Update mode requires the fixed root-owned installed script and recognized installation metadata.'
    fi
}

bootstrap_python_ready() {
    [[ -x /usr/bin/python3 ]] && /usr/bin/python3 -I -c \
        'import sys, ssl, tarfile, urllib.request, zlib; sys.exit(0 if sys.version_info >= (3, 8) else 1)' >/dev/null 2>&1
}

bootstrap_ca_ready() {
    [[ -r /etc/ssl/certs/ca-certificates.crt && -s /etc/ssl/certs/ca-certificates.crt ]] || return 1
    # If Python is absent, only the file check is possible in this pass. The
    # mandatory post-install pass validates its actual CA contents with OpenSSL.
    (( $1 )) || return 0
    /usr/bin/python3 -I -c \
        'import ssl, sys; ctx = ssl.create_default_context(cafile="/etc/ssl/certs/ca-certificates.crt"); sys.exit(0 if ctx.cert_store_stats()["x509_ca"] else 1)' >/dev/null 2>&1
}

bootstrap_account_tools_ready() {
    [[ -x /usr/sbin/useradd && -x /usr/sbin/groupadd ]]
}

bootstrap_nologin_ready() {
    [[ -x /usr/sbin/nologin ]]
}

bootstrap_missing() {
    PANEL_MISSING=()
    local python_ready=0
    if bootstrap_python_ready; then python_ready=1; else PANEL_MISSING+=(python3); fi
    bootstrap_ca_ready "$python_ready" || PANEL_MISSING+=(ca-certificates)
    bootstrap_account_tools_ready || PANEL_MISSING+=(passwd)
    bootstrap_nologin_ready || PANEL_MISSING+=(util-linux)
}

bootstrap_answer() {
    # Separate write/read descriptors work with a non-seekable controlling TTY.
    { printf '%s' 'Type yes to install only these dependencies: ' > /dev/tty &&
        IFS= read -r -n 16 REPLY < /dev/tty; } 2>/dev/null ||
        bootstrap_fail 'No controlling terminal; use --yes only for explicitly unattended operation.'
}

bootstrap_confirm() {
    printf 'Missing or unusable minimum dependencies: %s\n' "$*"
    printf '%s\n' 'APT will update its package index and install only this allowlist; no system upgrade or source changes.'
    if (( !PANEL_BOOTSTRAP_YES )); then
        local REPLY=''
        bootstrap_answer
        [[ $REPLY == yes ]] || bootstrap_fail 'Cancelled; packages, panel binary and service were not changed.'
    fi
}

bootstrap_apt() {
    local package
    (( $# )) || bootstrap_fail 'No dependencies were selected.'
    for package in "$@"; do
        case $package in python3|ca-certificates|passwd|util-linux) ;; *) bootstrap_fail 'Unapproved dependency package.' ;; esac
    done
    [[ -x /usr/bin/apt-get && -x /usr/bin/timeout && -x /usr/bin/env ]] ||
        bootstrap_fail 'The supported base system must provide apt-get and coreutils (env/timeout).'
    # Clean environment, fixed commands, bounded lock/retry/network and overall
    # deadlines. Never upgrade the system, rewrite sources or run downloaded code.
    /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/ LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l \
        /usr/bin/timeout --signal=TERM --kill-after=30s 180s /usr/bin/apt-get \
        -o DPkg::Lock::Timeout=30 -o Acquire::Retries=2 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 \
        -o APT::Update::Error-Mode=any update ||
        bootstrap_fail 'APT index update failed; the existing panel binary and service were not touched.'
    /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/ LANG=C LC_ALL=C DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l \
        /usr/bin/timeout --signal=TERM --kill-after=30s 300s /usr/bin/apt-get \
        -o DPkg::Lock::Timeout=30 -o Acquire::Retries=2 -o Acquire::http::Timeout=15 -o Acquire::https::Timeout=15 \
        install --yes --no-install-recommends "$@" ||
        bootstrap_fail 'APT dependency installation failed; the existing panel binary and service were not touched.'
}

bootstrap() {
    bootstrap_arguments "$@"
    bootstrap_root
    bootstrap_system
    bootstrap_update_source
    local attempted=' ' package round
    # At most two passes: installing absent Python may reveal a malformed CA
    # store. Never retry a dependency that an APT pass failed to make usable.
    for round in 1 2; do
        bootstrap_missing
        (( ${#PANEL_MISSING[@]} )) || return 0
        for package in "${PANEL_MISSING[@]}"; do
            [[ $attempted != *" $package "* ]] || bootstrap_fail "Dependency remains unavailable after APT: $package. Panel service/binary untouched."
        done
        bootstrap_confirm "${PANEL_MISSING[@]}"
        for package in "${PANEL_MISSING[@]}"; do attempted+="$package "; done
        bootstrap_apt "${PANEL_MISSING[@]}"
    done
    bootstrap_missing
    (( ${#PANEL_MISSING[@]} == 0 )) || bootstrap_fail "Dependency recheck failed: ${PANEL_MISSING[*]}. Panel service/binary untouched."
}

# Tests compile the definitions above in an isolated harness; there is no runtime
# environment/CLI switch for bypassing bootstrap or choosing production paths.
bootstrap "$@"
# The source descriptor comes from Bash, never from a user-supplied CLI option.
exec 4<"${BASH_SOURCE[0]}"
exec /usr/bin/python3 -I - "${BASH_SOURCE[0]}" "$@" <<'PANEL_PYTHON'
import argparse
import contextlib
import datetime
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import selectors
import signal
import ssl
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zlib

BINARY_LIMIT = 64 * 1024 * 1024
ARCHIVE_LIMIT = 64 * 1024 * 1024
TAR_LIMIT = BINARY_LIMIT + 10240
MANIFEST_LIMIT = 32 * 1024
SUMS_LIMIT = 16 * 1024
API_LIMIT = 1024 * 1024
SCRIPT_LIMIT = 128 * 1024
UNIT = "server-panel.service"
ACCOUNT = "server-panel"
LISTEN = "127.0.0.1:5173"
NAMES = {"panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "install.sh", "update.sh", "release.json", "SHA256SUMS"}
CDN_HOSTS = {"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"}
HEX = re.compile(r"[0-9a-f]{64}\Z", re.ASCII)
COMMIT = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z", re.ASCII)
VERSION = re.compile(r"(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\Z", re.ASCII)
WRAPPER = b'''#!/bin/bash -p
# Fixed, root-managed entry point; all arguments are validated by install.sh.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV PYTHONPATH PYTHONHOME
exec /bin/bash -p /usr/local/lib/server-panel/install.sh --update "$@"
'''


class Failure(Exception):
    pass


class Interrupted(BaseException):
    # Do not let health/network retry handlers swallow an administrator's signal.
    pass


def require(condition, message):
    if not condition:
        raise Failure(message)


def integer(value, low, high):
    return type(value) is int and low <= value <= high


def version_tuple(value, tagged=False):
    require(isinstance(value, str), "Version must be a stable semantic version.")
    if tagged:
        require(value.startswith("v"), "Release versions must have the v prefix.")
        value = value[1:]
    match = VERSION.fullmatch(value)
    require(match is not None, "Only stable vMAJOR.MINOR.PATCH releases are supported.")
    result = tuple(int(part) for part in match.groups())
    require(all(part <= 2147483647 for part in result), "Version component is too large.")
    return result


def repository(value):
    require(isinstance(value, str) and len(value) <= 201, "Invalid GitHub owner/repo.")
    parts = value.split("/")
    require(len(parts) == 2, "Use GitHub owner/repo, not a URL.")
    owner, repo = parts
    require(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?", owner, re.ASCII) is not None,
            "Invalid GitHub owner.")
    require(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?", repo, re.ASCII) is not None,
            "Invalid GitHub repository.")
    return value


def origin(value):
    if value == "":
        return "", LISTEN
    require(isinstance(value, str) and len(value) <= 300 and value.isascii(), "Origin must be an HTTPS ASCII domain.")
    require(not any(ord(char) <= 32 or ord(char) == 127 for char in value) and "%" not in value,
            "Invalid characters in origin.")
    try:
        parsed = urllib.parse.urlsplit(value)
        host, port = parsed.hostname, parsed.port
    except ValueError as exc:
        raise Failure("Invalid HTTPS origin.") from exc
    require(parsed.scheme == "https" and not parsed.username and not parsed.password and "@" not in parsed.netloc,
            "Origin must use HTTPS without credentials.")
    require(parsed.path in ("", "/") and not parsed.query and not parsed.fragment and "?" not in value and "#" not in value,
            "Origin cannot contain a path, query or fragment.")
    require(host is not None and len(host) <= 253 and "." in host and not host.endswith("."),
            "Origin requires a domain name, not an IP or localhost.")
    for label in host.split("."):
        require(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label, re.ASCII) is not None,
                "Invalid origin domain.")
    try:
        ipaddress.ip_address(host)
    except ValueError:
        pass
    else:
        raise Failure("Origin requires a domain name, not an IP address.")
    require(not parsed.netloc.endswith(":"), "Origin has an empty port.")
    require(port is None or 1 <= port <= 65535, "Invalid origin port.")
    netloc = host + (":" + str(port) if port not in (None, 443) else "")
    return "https://" + netloc, netloc


def strict_json(raw, limit):
    require(isinstance(raw, bytes) and len(raw) <= limit, "JSON exceeds its size limit.")

    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "Duplicate JSON field.")
            result[key] = value
        return result

    def constant(_):
        raise Failure("Non-finite JSON numbers are forbidden.")

    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=pairs, parse_constant=constant)
    except (ValueError, UnicodeError, RecursionError) as exc:
        raise Failure("Invalid UTF-8 JSON.") from exc
    require(type(value) is dict, "Expected a JSON object.")
    return value


def fields(value, expected):
    require(type(value) is dict and set(value) == set(expected), "Unexpected or missing metadata fields.")


def release_date(value):
    require(isinstance(value, str) and re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value, re.ASCII),
            "Build time must be UTC YYYY-MM-DDTHH:MM:SSZ.")
    try:
        datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError as exc:
        raise Failure("Invalid build time.") from exc


def canonical(repo, tag, name):
    repository(repo)
    version_tuple(tag, tagged=True)
    require(name in NAMES, "Unexpected release asset name.")
    return "https://github.com/" + repo + "/releases/download/" + tag + "/" + name


def validate_url(url, initial, api=False):
    require(isinstance(url, str) and len(url) <= 8192 and url.isascii()
            and not any(ord(char) <= 32 or ord(char) == 127 for char in url), "Invalid download URL.")
    try:
        parsed = urllib.parse.urlsplit(url)
        port = parsed.port
    except ValueError as exc:
        raise Failure("Invalid download URL port.") from exc
    require(parsed.scheme == "https" and parsed.hostname is not None and port in (None, 443)
            and not parsed.username and not parsed.password and "@" not in parsed.netloc
            and not parsed.fragment and "#" not in url, "Downloads require HTTPS without credentials or fragments.")
    if api:
        require(url == initial and parsed.netloc == "api.github.com", "GitHub API redirects are forbidden.")
    else:
        require(parsed.hostname in CDN_HOSTS, "Download redirect is outside the GitHub CDN allowlist.")
        if parsed.hostname == "github.com":
            require(url == initial, "GitHub asset path differs from the canonical release asset.")


class Redirects(urllib.request.HTTPRedirectHandler):
    def __init__(self, initial, api):
        self.initial, self.api, self.count = initial, api, 0

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        self.count += 1
        require(not self.api and self.count <= 5, "Too many redirects or forbidden API redirect.")
        validate_url(newurl, self.initial, self.api)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


@contextlib.contextmanager
def deadline(seconds):
    require(seconds > 0, "Operation deadline expired.")
    previous = signal.getsignal(signal.SIGALRM)

    def expired(_signum, _frame):
        raise Failure("Operation exceeded its time limit.")

    signal.signal(signal.SIGALRM, expired)
    signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)


class Downloads:
    # Dependency injection is an import-only testing API, never a CLI/environment option.
    def __init__(self, opener=None, timer=deadline):
        self.opener, self.timer = opener, timer

    def get(self, url, limit, timeout, destination=None, api=False):
        validate_url(url, url, api)
        require(0 < timeout <= (15 if api else 120), "Invalid network deadline.")
        handler = Redirects(url, api)
        opener = self.opener or urllib.request.build_opener(urllib.request.ProxyHandler({}), handler,
                    urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile="/etc/ssl/certs/ca-certificates.crt")))
        request = urllib.request.Request(url, headers={
            "Accept": "application/vnd.github+json" if api else "application/octet-stream",
            "Accept-Encoding": "identity", "User-Agent": "server-panel-installer/1",
        })
        digest, total, data = hashlib.sha256(), 0, bytearray()
        output = None
        try:
            if destination is not None:
                fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
                output = os.fdopen(fd, "wb")
            with self.timer(timeout), opener.open(request, timeout=min(timeout, 15)) as response:
                require(response.status == 200, "GitHub returned an unexpected HTTP status.")
                validate_url(response.geturl(), url, api)
                require(response.headers.get("Content-Encoding", "identity").lower() == "identity", "Encoded release transfers are forbidden.")
                length = response.headers.get("Content-Length")
                if length is not None:
                    require(re.fullmatch(r"[0-9]{1,12}", length, re.ASCII) is not None and int(length) <= limit,
                            "Download exceeds its size limit.")
                end = time.monotonic() + timeout
                while True:
                    require(time.monotonic() < end, "Download exceeded its time limit.")
                    chunk = response.read1(min(65536, limit + 1 - total))
                    if not chunk:
                        break
                    total += len(chunk)
                    require(total <= limit, "Download exceeds its size limit.")
                    digest.update(chunk)
                    if output is None:
                        data.extend(chunk)
                    else:
                        output.write(chunk)
                require(length is None or total == int(length), "Incomplete download.")
            if output is not None:
                output.flush()
                os.fsync(output.fileno())
                return total, digest.hexdigest()
            return bytes(data)
        except (urllib.error.URLError, OSError, http.client.HTTPException) as exc:
            raise Failure("GitHub HTTPS transfer failed; TLS verification was not disabled.") from exc
        finally:
            if output is not None:
                output.close()


def release_api(raw, repo, requested):
    value = strict_json(raw, API_LIMIT)
    require(value.get("draft") is False and value.get("prerelease") is False, "A published stable release is required.")
    tag = value.get("tag_name")
    version_tuple(tag, tagged=True)
    require(requested is None or tag == requested, "GitHub returned the wrong release tag.")
    require(value.get("html_url") == "https://github.com/" + repo + "/releases/tag/" + tag,
            "GitHub returned a non-canonical Release URL.")
    assets = value.get("assets")
    require(type(assets) is list and len(assets) == len(NAMES), "Release must contain the six contracted assets.")
    sizes = {}
    for asset in assets:
        require(type(asset) is dict and isinstance(asset.get("name"), str), "Invalid release asset.")
        name = asset["name"]
        require(name in NAMES and name not in sizes, "Unknown or duplicate release asset.")
        maximum = ARCHIVE_LIMIT if name.startswith("panel-linux-") else {"release.json": MANIFEST_LIMIT, "SHA256SUMS": SUMS_LIMIT,
                                                                       "install.sh": SCRIPT_LIMIT, "update.sh": SUMS_LIMIT}[name]
        require(integer(asset.get("size"), 1, maximum), "Invalid release asset size.")
        require(asset.get("browser_download_url") == canonical(repo, tag, name), "Non-canonical asset download URL.")
        require(asset.get("state", "uploaded") == "uploaded", "A release asset is not fully uploaded.")
        sizes[name] = asset["size"]
    require(set(sizes) == NAMES, "Missing release assets.")
    return tag, sizes


def checksums(raw):
    require(isinstance(raw, bytes) and 0 < len(raw) <= SUMS_LIMIT and raw.endswith(b"\n"), "Invalid SHA256SUMS.")
    try:
        lines = raw.decode("ascii").splitlines(keepends=True)
    except UnicodeError as exc:
        raise Failure("SHA256SUMS must be ASCII.") from exc
    result = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)\n", line, re.ASCII)
        require(match is not None, "Invalid SHA256SUMS line.")
        digest, name = match.groups()
        require(name in NAMES - {"SHA256SUMS"} and name not in result, "Unknown or duplicate checksum asset.")
        result[name] = digest
    require(set(result) == NAMES - {"SHA256SUMS"}, "SHA256SUMS does not cover exactly the expected assets.")
    return result


def manifest(raw, sums, repo, tag):
    require(hashlib.sha256(raw).hexdigest() == sums["release.json"], "Manifest SHA-256 mismatch.")
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "version", "tag", "repository", "dataSchema", "commit", "buildTime", "binaries"))
    require(integer(value["format"], 2, 2) and integer(value["dataSchema"], 1, 1), "Only manifest format 2 and data schema 1 are supported; format 1 bare binaries are not accepted.")
    version_tuple(value["version"])
    require(value["tag"] == tag and value["version"] == tag[1:] and value["repository"] == repo, "Manifest release identity mismatch.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid release commit.")
    release_date(value["buildTime"])
    binaries = value["binaries"]
    require(type(binaries) is list and len(binaries) == 2, "Manifest must describe exactly amd64 and arm64.")
    seen = set()
    for binary in binaries:
        fields(binary, ("os", "arch", "name", "size", "sha256", "archive"))
        arch = binary["arch"]
        require(isinstance(arch, str) and arch in ("amd64", "arm64") and arch not in seen, "Invalid manifest architecture.")
        seen.add(arch)
        require(binary["os"] == "linux" and binary["name"] == "panel", "Invalid inner binary identity.")
        require(integer(binary["size"], 1, BINARY_LIMIT), "Invalid binary size.")
        require(isinstance(binary["sha256"], str) and HEX.fullmatch(binary["sha256"]), "Invalid inner binary SHA-256.")
        archive = binary["archive"]
        fields(archive, ("name", "size", "sha256"))
        require(archive["name"] == "panel-linux-" + arch + ".tar.gz", "Invalid archive asset identity.")
        require(integer(archive["size"], 1, ARCHIVE_LIMIT), "Invalid archive size.")
        require(isinstance(archive["sha256"], str) and HEX.fullmatch(archive["sha256"])
                and archive["sha256"] == sums[archive["name"]], "Archive manifest/checksum mismatch.")
    return value


def gzip_chunks(stream):
    # zlib's max_length bounds allocation BEFORE decompression. gzip.GzipFile also
    # accepts concatenated members, which are deliberately not part of format 2.
    decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
    compressed = expanded = 0
    try:
        while True:
            chunk = stream.read(min(65536, ARCHIVE_LIMIT + 1 - compressed))
            if not chunk:
                break
            compressed += len(chunk)
            require(compressed <= ARCHIVE_LIMIT, "Archive exceeds its compressed size limit.")
            while chunk:
                raw = decoder.decompress(chunk, min(65536, TAR_LIMIT + 1 - expanded))
                expanded += len(raw)
                require(expanded <= TAR_LIMIT, "Archive expansion exceeds its tar size limit.")
                if raw:
                    yield raw
                if decoder.eof:
                    require(not decoder.unused_data and not stream.read(1), "Trailing data or multiple gzip members are forbidden.")
                    return
                chunk = decoder.unconsumed_tail
        require(decoder.eof, "Truncated gzip archive.")
    except zlib.error as exc:
        raise Failure("Invalid gzip archive integrity.") from exc


def extract_panel(stream, candidate, expected):
    # Parse exactly one raw tar header, not TarFile's extension-aware iterator.
    # Never use an archive-supplied path to open a file. The caller supplies the
    # fixed staging path, created exclusively and non-executable until verified.
    header, digest = bytearray(), hashlib.sha256()
    remaining, padding, trailer, output = None, 0, 0, None
    written = 0
    try:
        for chunk in gzip_chunks(stream):
            if remaining is None:
                count = min(512 - len(header), len(chunk))
                header.extend(chunk[:count])
                chunk = chunk[count:]
                if len(header) < 512:
                    continue
                raw_header = bytes(header)
                try:
                    member = tarfile.TarInfo.frombuf(raw_header, "ascii", "strict")
                except (tarfile.TarError, ValueError, UnicodeError) as exc:
                    raise Failure("Invalid tar header.") from exc
                require(raw_header[:100] == b"panel" + b"\0" * 95 and member.name == "panel"
                        and raw_header[345:500] == b"\0" * 155, "Archive must contain only the top-level panel path.")
                require(member.type == tarfile.REGTYPE and member.mode == 0o755
                        and raw_header[157:257] == b"\0" * 100, "Archive panel must be a mode 0755 regular file, not a link or extension.")
                require(raw_header[257:265] == b"ustar\00000", "Only a plain USTAR header is supported.")
                require(integer(member.size, 1, BINARY_LIMIT) and member.size == expected["size"], "Inner binary tar/manifest size mismatch.")
                remaining, padding = member.size, (-member.size) % 512
                fd = os.open(candidate, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
                output = os.fdopen(fd, "wb")
            count = min(remaining, len(chunk))
            if count:
                output.write(chunk[:count])
                digest.update(chunk[:count])
                written += count
                remaining -= count
                chunk = chunk[count:]
            if chunk:
                count = min(padding, len(chunk))
                require(not any(chunk[:count]), "Nonzero tar file padding is forbidden.")
                padding -= count
                chunk = chunk[count:]
                require(not any(chunk), "Extra tar members or trailing nonzero data are forbidden.")
                trailer += len(chunk)
        require(output is not None and remaining == 0 and padding == 0
                and trailer >= 1024 and trailer % 512 == 0, "Truncated tar archive or missing end blocks.")
        require(written == expected["size"] and digest.hexdigest() == expected["sha256"], "Inner binary SHA-256/size mismatch.")
        output.flush()
        os.fsync(output.fileno())
        return written, digest.hexdigest()
    finally:
        if output is not None:
            output.close()


def build_info(raw, repo, arch, expected=None):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("version", "commit", "buildTime", "repository", "dataSchema", "os", "arch"))
    version_tuple(value["version"])
    require(integer(value["dataSchema"], 1, 1) and value["os"] == "linux" and value["arch"] == arch
            and value["repository"] == repo, "Binary build-info repository/platform/schema mismatch.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid binary commit.")
    release_date(value["buildTime"])
    if expected is not None:
        for key in ("version", "commit", "buildTime", "repository", "dataSchema"):
            require(value[key] == expected[key], "Binary build-info differs from verified release metadata.")
    return value


class Paths:
    binary = Path("/usr/local/bin/panel")
    previous = Path("/usr/local/bin/panel.previous")
    code_dir = Path("/usr/local/lib/server-panel")
    installer = code_dir / "install.sh"
    updater = Path("/usr/local/bin/panel-update")
    config_dir = Path("/etc/server-panel")
    config = config_dir / "install.json"
    transaction = config_dir / "update-transaction.json"
    unit = Path("/etc/systemd/system/server-panel.service")
    data = Path("/var/lib/server-panel")
    lock_dir = Path("/run/server-panel-update")
    lock = lock_dir / "lock"


class SafeFiles:
    def info(self, path):
        return os.lstat(path)

    def owner_mode(self, path, uid, gid, mode):
        os.chown(path, uid, gid, follow_symlinks=False)
        os.chmod(path, mode, follow_symlinks=False)

    def parents(self, path):
        for parent in reversed(Path(path).parents):
            entry = self.info(parent)
            require(stat.S_ISDIR(entry.st_mode) and entry.st_uid == 0 and not entry.st_mode & 0o022,
                    "A managed path has a symlink, non-root owner or writable parent: " + str(parent))

    def check(self, path, directory=False, mode=None, uid=0, gid=0):
        self.parents(path)
        entry = self.info(path)
        expected_type = stat.S_ISDIR if directory else stat.S_ISREG
        require(expected_type(entry.st_mode) and not stat.S_ISLNK(entry.st_mode) and entry.st_uid == uid and entry.st_gid == gid,
                "Unsafe type or owner at " + str(path))
        require(not entry.st_mode & 0o022 and (mode is None or stat.S_IMODE(entry.st_mode) == mode), "Unsafe permissions at " + str(path))
        require(directory or entry.st_nlink == 1, "Hard-linked management files are forbidden: " + str(path))
        return entry

    def exists(self, path):
        return os.path.lexists(path)

    def mkdir(self, path, mode=0o755, uid=0, gid=0):
        self.parents(path)
        if self.exists(path):
            self.check(path, directory=True, mode=mode, uid=uid, gid=gid)
            return
        os.mkdir(path, 0o700)
        self.owner_mode(path, uid, gid, mode)
        self.check(path, directory=True, mode=mode, uid=uid, gid=gid)

    def read(self, path, limit, mode=None):
        entry = self.check(path, mode=mode)
        require(entry.st_size <= limit, "Management file exceeds size limit: " + str(path))
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            require((opened.st_dev, opened.st_ino) == (entry.st_dev, entry.st_ino), "Management file changed while opening.")
            raw = stream.read(limit + 1)
        require(len(raw) <= limit, "Management file exceeds size limit.")
        return raw

    def sync_dir(self, path):
        fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def replace(self, source, target):
        self.parents(target)
        if self.exists(target):
            self.check(target)
        self.check(source)
        require(self.info(source).st_dev == self.info(target.parent).st_dev, "Atomic replacement requires the same filesystem.")
        # Flush both file data and its final ownership/mode before publishing the rename.
        fd = os.open(source, os.O_RDWR | getattr(os, "O_NOFOLLOW", 0))
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(source, target)
        self.sync_dir(target.parent)

    def atomic_bytes(self, path, data, mode):
        self.parents(path)
        if self.exists(path):
            self.check(path, mode=mode)
        fd, name = tempfile.mkstemp(prefix=".panel-write-", dir=path.parent)
        temporary = Path(name)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(data)
                stream.flush()
                self.owner_mode(temporary, 0, 0, mode)
                os.fsync(stream.fileno())
            self.replace(temporary, path)
        finally:
            if self.exists(temporary):
                os.unlink(temporary)

    def copy_binary(self, source, destination, expected_hash):
        raw = self.read(source, BINARY_LIMIT, mode=0o755)
        require(hashlib.sha256(raw).hexdigest() == expected_hash, "Current/previous binary SHA-256 mismatch.")
        require(not self.exists(destination), "Staging destination already exists.")
        self.atomic_bytes(destination, raw, 0o755)

    @contextlib.contextmanager
    def staging(self, parent):
        self.parents(parent / "unused")
        directory = Path(tempfile.mkdtemp(prefix=".server-panel-stage-", dir=parent))
        self.owner_mode(directory, 0, 0, 0o700)
        try:
            yield directory
        finally:
            # Only our freshly created flat staging directory is removed, never panel data.
            for name in directory.iterdir():
                if name.is_file() or name.is_symlink():
                    name.unlink()
            directory.rmdir()


def unit_bytes(config):
    repo = repository(config["repository"])
    normalized, host = origin(config["origin"])
    require(normalized == config["origin"] and host == config["healthHost"], "Non-normalized installation origin.")
    command = "/usr/local/bin/panel -listen 127.0.0.1:5173 -data-dir /var/lib/server-panel -github-repo " + repo
    if normalized:
        command += " -origin " + normalized
    require("%" not in command and "\n" not in command, "Invalid unit arguments.")
    return ("""[Unit]
Description=Single-process server panel
After=network.target

[Service]
Type=simple
User=server-panel
Group=server-panel
StateDirectory=server-panel
StateDirectoryMode=0700
WorkingDirectory=/var/lib/server-panel
ExecStart=""" + command + """
Restart=on-failure
RestartSec=5s
TimeoutStartSec=30s
TimeoutStopSec=15s
UMask=0077
Nice=10
CPUWeight=20
IOWeight=20
MemoryHigh=80M
MemoryMax=128M
TasksMax=96
LimitNOFILE=512
Environment=GOMEMLIMIT=64MiB
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
RestrictRealtime=true
RestrictNamespaces=true
CapabilityBoundingSet=
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
""").encode("utf-8")


def config_bytes(config):
    return (json.dumps(config, ensure_ascii=True, indent=2, sort_keys=True) + "\n").encode("utf-8")


def installation_config(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "repository", "listen", "origin", "healthHost", "version", "dataSchema", "commit", "buildTime",
                   "binarySHA256", "installerSHA256"))
    require(integer(value["format"], 1, 1) and integer(value["dataSchema"], 1, 1), "Unsupported installation format/data schema.")
    repository(value["repository"])
    version_tuple(value["version"])
    require(value["listen"] == LISTEN, "Installation listener is not the fixed loopback address.")
    normalized, host = origin(value["origin"])
    require(normalized == value["origin"] and host == value["healthHost"], "Installation origin/health Host mismatch.")
    for key in ("binarySHA256", "installerSHA256"):
        require(isinstance(value[key], str) and HEX.fullmatch(value[key]), "Invalid installation SHA-256.")
    require(isinstance(value["commit"], str) and COMMIT.fullmatch(value["commit"]), "Invalid installed commit.")
    release_date(value["buildTime"])
    return value


def update_transaction(raw):
    value = strict_json(raw, MANIFEST_LIMIT)
    fields(value, ("format", "phase", "old", "new"))
    require(integer(value["format"], 1, 1) and value["phase"] in ("prepared", "ready"), "Invalid update transaction format/phase.")
    old = installation_config(config_bytes(value["old"]))
    new = installation_config(config_bytes(value["new"]))
    for key in ("format", "repository", "listen", "origin", "healthHost", "dataSchema", "installerSHA256"):
        require(old[key] == new[key], "Update transaction changes fixed installation settings.")
    require(version_tuple(new["version"]) > version_tuple(old["version"])
            and old["binarySHA256"] != new["binarySHA256"], "Invalid update transaction version/digest transition.")
    return value


class Runtime:
    def preflight(self, files):
        require(sys.platform == "linux" and os.geteuid() == 0, "Management requires root on systemd Linux.")
        arch = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
        require(arch is not None, "Only Linux amd64/arm64 are supported.")
        require(sys.version_info >= (3, 8), "Python 3.8 or newer is required after dependency bootstrap.")
        files.check(Path("/run/systemd/system"), directory=True)
        try:
            trust = ssl.create_default_context(cafile="/etc/ssl/certs/ca-certificates.crt")
            require(trust.cert_store_stats()["x509_ca"] > 0, "A usable system CA store is required after dependency bootstrap.")
        except (OSError, ssl.SSLError) as exc:
            raise Failure("A usable system CA store is required after dependency bootstrap.") from exc
        for tool in ("/usr/bin/systemctl", "/usr/sbin/useradd", "/usr/sbin/groupadd", "/usr/sbin/nologin"):
            entry = files.check(Path(tool))
            require(entry.st_mode & 0o111, "Required tool is not executable: " + tool)
        return arch

    def run(self, arguments, timeout=25):
        # No shell, inherited environment, unbounded stdout or orphan process on timeout.
        process = subprocess.Popen([str(part) for part in arguments], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, cwd="/", env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LANG": "C", "LC_ALL": "C", "HOME": "/"},
                                   close_fds=True, start_new_session=True)
        stdout, stderr = bytearray(), bytearray()
        end = time.monotonic() + timeout
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ, stdout)
        selector.register(process.stderr, selectors.EVENT_READ, stderr)
        try:
            while selector.get_map():
                remaining = end - time.monotonic()
                require(remaining > 0, "A management subprocess timed out.")
                for key, _ in selector.select(min(remaining, 0.5)):
                    chunk = os.read(key.fileobj.fileno(), 8192)
                    if chunk:
                        key.data.extend(chunk)
                        require(len(stdout) + len(stderr) <= 65536, "Management subprocess output limit exceeded.")
                    else:
                        selector.unregister(key.fileobj)
            remaining = end - time.monotonic()
            require(remaining > 0, "A management subprocess timed out.")
            code = process.wait(timeout=remaining)
            return code, bytes(stdout)
        except BaseException:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            process.wait(timeout=5)
            raise
        finally:
            selector.close()
            process.stdout.close()
            process.stderr.close()

    def systemctl(self, verb, *options, timeout=25, checked=True):
        require(verb in {"show", "stop", "start", "enable", "disable", "is-active", "daemon-reload"}, "Unexpected service operation.")
        arguments = ["/usr/bin/systemctl", verb]
        if verb != "daemon-reload":
            arguments.extend(options)
            arguments.append(UNIT)
        code, output = self.run(arguments, timeout)
        require(not checked or code == 0, "systemctl " + verb + " failed.")
        return code, output

    def inspect_unit(self, expected):
        _, raw = self.systemctl("show", "--property=LoadState", "--property=FragmentPath", "--property=DropInPaths", "--no-pager", checked=False)
        try:
            entries = dict(line.split("=", 1) for line in raw.decode("utf-8").strip().splitlines())
        except (ValueError, UnicodeError) as exc:
            raise Failure("Unable to inspect the fixed service unit.") from exc
        require(set(entries) == {"LoadState", "FragmentPath", "DropInPaths"} and not entries["DropInPaths"],
                "Unknown service unit overrides are not adopted.")
        if expected is None:
            require(entries["LoadState"] == "not-found" and not entries["FragmentPath"], "An unknown server-panel unit already exists.")
        else:
            require(entries["LoadState"] == "loaded" and entries["FragmentPath"] == str(expected), "The fixed service unit is not loaded from the managed path.")

    def account(self, data, create=False):
        import grp
        import pwd
        try:
            user = pwd.getpwnam(ACCOUNT)
        except KeyError:
            user = None
        try:
            group = grp.getgrnam(ACCOUNT)
        except KeyError:
            group = None
        if create:
            require(user is None and group is None, "An unknown service account/group already exists.")
            code, _ = self.run(["/usr/sbin/groupadd", "--system", ACCOUNT])
            require(code == 0, "Could not create the fixed service group.")
            code, _ = self.run(["/usr/sbin/useradd", "--system", "--gid", ACCOUNT, "--home-dir", str(data),
                                "--no-create-home", "--shell", "/usr/sbin/nologin", "--password", "!", ACCOUNT])
            require(code == 0, "Could not create the fixed service account.")
            user, group = pwd.getpwnam(ACCOUNT), grp.getgrnam(ACCOUNT)
        if user is None and group is None:
            return None
        require(user is not None and group is not None and user.pw_uid > 0 and group.gr_gid > 0
                and user.pw_gid == group.gr_gid and user.pw_shell == "/usr/sbin/nologin" and user.pw_dir == str(data),
                "Unsafe or unknown service account/group.")
        require(set(os.getgrouplist(ACCOUNT, group.gr_gid)) == {group.gr_gid} and not set(group.gr_mem) - {ACCOUNT},
                "The service account must not have supplementary privileges or shared group members.")
        return user.pw_uid, group.gr_gid

    @contextlib.contextmanager
    def lock(self, paths, files):
        import fcntl
        files.mkdir(paths.lock_dir, mode=0o700)
        if files.exists(paths.lock):
            files.check(paths.lock, mode=0o600)
        fd = os.open(paths.lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
        try:
            files.check(paths.lock, mode=0o600)
            opened = os.fstat(fd)
            current = files.info(paths.lock)
            require((opened.st_dev, opened.st_ino) == (current.st_dev, current.st_ino), "Update lock changed while opening.")
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as exc:
                raise Failure("Another installation/update holds the independent management lock.") from exc
            yield
        finally:
            os.close(fd)

    def binary_info(self, path):
        code, raw = self.run([path, "-build-info"], timeout=10)
        require(code == 0, "Verified binary -build-info failed.")
        return raw

    def confirm(self, message, unattended):
        print(message, flush=True)
        print("Trust boundary: the configured public GitHub repository and HTTPS. SHA-256 is not a publisher signature.", flush=True)
        if unattended:
            return
        try:
            # A TTY is non-seekable: separate streams avoid BufferedRandom's r+ seek requirement.
            # stdin contains the Python heredoc, so it cannot be used for confirmation.
            with open("/dev/tty", "w", encoding="utf-8") as terminal:
                terminal.write("Type yes to continue: ")
                terminal.flush()
            with open("/dev/tty", "r", encoding="utf-8") as terminal:
                answer = terminal.readline(16)
        except OSError as exc:
            raise Failure("No controlling terminal; use --yes only for explicitly unattended operation.") from exc
        require(answer.strip() == "yes", "Cancelled; installation state was not changed.")

    def health_once(self, config, timeout):
        connection = http.client.HTTPConnection("127.0.0.1", 5173, timeout=min(2, timeout))
        try:
            with deadline(min(2, timeout)):
                connection.request("GET", "/healthz", headers={"Host": config["healthHost"], "Connection": "close"})
                response = connection.getresponse()
                require(response.status == 200, "Health endpoint did not return HTTP 200.")
                value = strict_json(response.read(4097), 4096)
                fields(value, ("status", "version", "dataSchema"))
                return value["status"] == "ok" and value["version"] == config["version"] and integer(value["dataSchema"], 1, 1)
        except (Failure, OSError, http.client.HTTPException):
            return False
        finally:
            connection.close()

    def healthy(self, config):
        end = time.monotonic() + 30
        while time.monotonic() < end:
            if self.health_once(config, end - time.monotonic()):
                code, _ = self.systemctl("is-active", "--quiet", timeout=max(0.01, min(5, end - time.monotonic())), checked=False)
                if code == 0:
                    return
            time.sleep(max(0, min(0.5, end - time.monotonic())))
        raise Failure("Service did not become healthy with the expected version and schema within 30 seconds.")

    @contextlib.contextmanager
    def rollback_signals(self):
        previous = {}
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            previous[signum] = signal.signal(signum, signal.SIG_IGN)
        try:
            yield
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)


def read_source(source, installed, files, updating):
    path = Path(os.path.abspath(source))
    if updating:
        require(path == installed, "Update mode may run only from the fixed root-owned installed script.")
        files.check(path, mode=0o755)
    else:
        sudo_uid = os.environ.get("SUDO_UID", "0")
        require(re.fullmatch(r"[0-9]{1,10}", sudo_uid, re.ASCII), "Invalid sudo source identity.")
        allowed = {0, int(sudo_uid)}
        for item in [*reversed(path.parents), path]:
            entry = os.lstat(item)
            require(not stat.S_ISLNK(entry.st_mode) and entry.st_uid in allowed and not entry.st_mode & 0o022,
                    "The reviewed installer must have trusted ownership, no symlinks and no group/world-writable source paths.")
    entry = os.lstat(path)
    opened = os.fstat(4)
    require(stat.S_ISREG(entry.st_mode) and entry.st_nlink == 1 and entry.st_size <= SCRIPT_LIMIT
            and (entry.st_dev, entry.st_ino) == (opened.st_dev, opened.st_ino), "Untrusted installer source descriptor.")
    with os.fdopen(os.dup(4), "rb") as stream:
        raw = stream.read(SCRIPT_LIMIT + 1)
    require(len(raw) <= SCRIPT_LIMIT and raw.startswith(b"#!/bin/bash\n") and raw.endswith(b"PANEL_PYTHON\n"),
            "Installer source must be this complete reviewed Bash/Python script.")
    return raw


class Manager:
    def __init__(self, paths=None, files=None, runtime=None, downloads=None):
        self.paths = paths or Paths()
        self.files = files or SafeFiles()
        self.runtime = runtime or Runtime()
        self.downloads = downloads or Downloads()

    def inspect_management(self, config):
        p, f, r = self.paths, self.files, self.runtime
        f.check(p.code_dir, directory=True, mode=0o755)
        f.check(p.config_dir, directory=True, mode=0o755)
        require(hashlib.sha256(f.read(p.installer, SCRIPT_LIMIT, mode=0o755)).hexdigest() == config["installerSHA256"],
                "Installed management script was modified; updates never silently replace it.")
        require(f.read(p.updater, SUMS_LIMIT, mode=0o755) == WRAPPER, "Unknown or modified panel-update wrapper.")
        require(f.read(p.unit, SUMS_LIMIT, mode=0o644) == unit_bytes(config), "Unknown or modified service unit.")
        if f.exists(p.previous):
            f.check(p.previous, mode=0o755)
        identity = r.account(p.data)
        require(identity is not None, "The fixed service account is missing.")
        f.check(p.data, directory=True, mode=0o700, uid=identity[0], gid=identity[1])
        r.inspect_unit(p.unit)

    def inspect(self, arch):
        p, f, r = self.paths, self.files, self.runtime
        f.check(p.config_dir, directory=True, mode=0o755)
        raw = f.read(p.config, MANIFEST_LIMIT, mode=0o644)
        config = installation_config(raw)
        self.inspect_management(config)
        require(hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest() == config["binarySHA256"],
                "Installed binary SHA-256 mismatch.")
        current = build_info(r.binary_info(p.binary), config["repository"], arch, config)
        return config, raw, current

    def clear_transaction(self, raw):
        p, f = self.paths, self.files
        require(f.read(p.transaction, MANIFEST_LIMIT, mode=0o600) == raw, "Update transaction changed before completion.")
        try:
            os.unlink(p.transaction)
            f.sync_dir(p.transaction.parent)
        except BaseException:
            # If unlink succeeded but its fsync failed, preserve a retryable marker where possible.
            if not f.exists(p.transaction):
                f.atomic_bytes(p.transaction, raw, 0o600)
            raise

    def recover_transaction(self, arch):
        p, f, r = self.paths, self.files, self.runtime
        if not f.exists(p.transaction):
            return False
        f.check(p.config_dir, directory=True, mode=0o755)
        raw = f.read(p.transaction, MANIFEST_LIMIT, mode=0o600)
        transaction = update_transaction(raw)
        old, new = transaction["old"], transaction["new"]
        config = installation_config(f.read(p.config, MANIFEST_LIMIT, mode=0o644))
        require(config == old or (transaction["phase"] == "ready" and config == new),
                "Update transaction does not match installed metadata; refusing recovery.")
        self.inspect_management(old)
        current_digest = hashlib.sha256(f.read(p.binary, BINARY_LIMIT, mode=0o755)).hexdigest()
        if current_digest == old["binarySHA256"]:
            # prepared can be interrupted before panel.previous exists; current is still the verified old binary.
            source = p.binary
        else:
            require(transaction["phase"] == "ready" and current_digest == new["binarySHA256"],
                    "Unknown current binary or incomplete backup phase; refusing transaction recovery.")
            source = p.previous
            require(hashlib.sha256(f.read(source, BINARY_LIMIT, mode=0o755)).hexdigest() == old["binarySHA256"],
                    "Recovery previous binary SHA-256 mismatch.")
        # Never execute the new or an arbitrary marker-specified path during recovery.
        build_info(r.binary_info(source), old["repository"], arch, old)
        print("Recovering an interrupted update to the verified previous version v" + old["version"] + ".", flush=True)
        try:
            with r.rollback_signals():
                with f.staging(p.binary.parent) as directory:
                    restored = None
                    if source == p.previous:
                        restored = directory / "rollback"
                        f.copy_binary(source, restored, old["binarySHA256"])
                    # Stop may already have completed before its subprocess reported an error.
                    with contextlib.suppress(Failure):
                        r.systemctl("stop")
                    if restored is not None:
                        f.replace(restored, p.binary)
                    r.systemctl("start")
                    r.healthy(old)
                    f.atomic_bytes(p.config, config_bytes(old), 0o644)
                self.clear_transaction(raw)
        except BaseException as exc:
            raise Failure("Interrupted update recovery failed; the transaction marker and previous binary are retained for retry. "
                          "Account data was not reset. Cause: " + type(exc).__name__) from exc
        return True

    def initial_check(self):
        p, f, r = self.paths, self.files, self.runtime
        for path in (p.binary, p.previous, p.code_dir, p.updater, p.config_dir, p.unit, p.data):
            f.parents(path)
            require(not f.exists(path), "Unknown or partial installation exists; refusing adoption/overwrite: " + str(path))
        require(r.account(p.data) is None, "An unknown service account already exists.")
        r.inspect_unit(None)

    def target(self, repo, requested):
        suffix = "latest" if requested is None else "tags/" + requested
        url = "https://api.github.com/repos/" + repository(repo) + "/releases/" + suffix
        return release_api(self.downloads.get(url, API_LIMIT, 15, api=True), repo, requested)

    def stage_release(self, directory, repo, tag, sizes, arch):
        end = time.monotonic() + 120

        def fetch(name, limit, destination=None):
            remaining = end - time.monotonic()
            require(remaining > 0, "The total release transfer deadline expired.")
            result = self.downloads.get(canonical(repo, tag, name), limit, remaining, destination=destination)
            actual_size = len(result) if destination is None else result[0]
            require(actual_size == sizes[name], "Downloaded asset size differs from GitHub metadata.")
            return result

        sums = checksums(fetch("SHA256SUMS", SUMS_LIMIT))
        metadata = manifest(fetch("release.json", MANIFEST_LIMIT), sums, repo, tag)
        selected = next(binary for binary in metadata["binaries"] if binary["arch"] == arch)
        require(all(binary["archive"]["size"] == sizes[binary["archive"]["name"]] for binary in metadata["binaries"]),
                "Archive API/manifest size mismatch.")
        archive = selected["archive"]
        archive_path, candidate = directory / archive["name"], directory / "panel"
        size, archive_digest = fetch(archive["name"], archive["size"], archive_path)
        require(size == archive["size"] and archive_digest == archive["sha256"], "Downloaded archive SHA-256/size mismatch.")
        entry = self.files.check(archive_path, mode=0o600)
        fd = os.open(archive_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
        with os.fdopen(fd, "rb") as stream:
            opened = os.fstat(stream.fileno())
            require((opened.st_dev, opened.st_ino) == (entry.st_dev, entry.st_ino), "Staged archive changed while opening.")
            total, staged_hash = 0, hashlib.sha256()
            while True:
                chunk = stream.read(min(65536, archive["size"] + 1 - total))
                if not chunk:
                    break
                total += len(chunk)
                require(total <= archive["size"], "Staged archive size changed after download.")
                staged_hash.update(chunk)
            require(total == archive["size"] and staged_hash.hexdigest() == archive_digest, "Staged archive SHA-256/size changed after download.")
            # Verify the actual on-disk compressed bytes before attempting expansion.
            stream.seek(0)
            _, digest = extract_panel(stream, candidate, selected)
        self.files.owner_mode(candidate, 0, 0, 0o755)
        raw = self.files.read(candidate, BINARY_LIMIT, mode=0o755)
        require(len(raw) == selected["size"] and hashlib.sha256(raw).hexdigest() == digest, "Staged binary changed after extraction.")
        require(len(raw) >= 20 and raw[:6] == b"\x7fELF\x02\x01" and int.from_bytes(raw[18:20], "little") == {"amd64": 62, "arm64": 183}[arch],
                "The release binary is not the expected Linux ELF architecture.")
        build_info(self.runtime.binary_info(candidate), repo, arch, metadata)
        return candidate, metadata, digest

    def apply_update(self, directory, candidate, old, old_raw, new):
        p, f, r = self.paths, self.files, self.runtime
        require(not f.exists(p.transaction), "An unfinished update transaction must be recovered first.")
        transaction = {"format": 1, "phase": "prepared", "old": old, "new": new}
        raw = config_bytes(transaction)
        update_transaction(raw)
        # Durable intent before touching the backup. No downloaded path is stored in this marker.
        f.atomic_bytes(p.transaction, raw, 0o600)
        backup = directory / "previous"
        f.copy_binary(p.binary, backup, old["binarySHA256"])
        f.replace(backup, p.previous)
        transaction["phase"] = "ready"
        raw = config_bytes(transaction)
        # A durable ready marker means panel.previous was already fsynced before any stop/switch.
        f.atomic_bytes(p.transaction, raw, 0o600)
        try:
            r.systemctl("stop")
            f.replace(candidate, p.binary)
            r.systemctl("start")
            r.healthy(new)
            f.atomic_bytes(p.config, config_bytes(new), 0o644)
            self.clear_transaction(raw)
        except BaseException as update_error:
            with r.rollback_signals():
                try:
                    # A stop error does not prevent trying binary restoration and an explicit old-version probe.
                    with contextlib.suppress(Failure):
                        r.systemctl("stop")
                    restored = directory / "rollback"
                    f.copy_binary(p.previous, restored, old["binarySHA256"])
                    f.replace(restored, p.binary)
                    r.systemctl("start")
                    r.healthy(old)
                    f.atomic_bytes(p.config, old_raw, 0o644)
                    self.clear_transaction(raw)
                except BaseException as rollback_error:
                    raise Failure("Update failed; rollback also failed. Previous binary is preserved at " + str(p.previous)
                                  + "; the transaction marker is retained for the next invocation. Data was not reset. Cause: "
                                  + type(update_error).__name__ + "; rollback: " + type(rollback_error).__name__) from rollback_error
            raise Failure("Update failed; rollback succeeded and the previous version is healthy. Data was not reset. Cause: "
                          + type(update_error).__name__) from update_error

    def apply_install(self, candidate, config, source):
        p, f, r = self.paths, self.files, self.runtime
        unit_written = False
        try:
            uid, gid = r.account(p.data, create=True)
            f.mkdir(p.code_dir)
            f.mkdir(p.config_dir)
            f.mkdir(p.data, mode=0o700, uid=uid, gid=gid)
            f.atomic_bytes(p.installer, source, 0o755)
            f.atomic_bytes(p.updater, WRAPPER, 0o755)
            f.atomic_bytes(p.unit, unit_bytes(config), 0o644)
            unit_written = True
            f.replace(candidate, p.binary)
            r.systemctl("daemon-reload")
            r.systemctl("start")
            r.healthy(config)
            r.systemctl("enable")
            f.atomic_bytes(p.config, config_bytes(config), 0o644)
        except BaseException as exc:
            stop_result = "service was not started"
            with r.rollback_signals():
                if unit_written:
                    try:
                        r.systemctl("stop")
                        r.systemctl("disable")
                        stop_result = "service stopped and disabled"
                    except BaseException:
                        stop_result = "could not confirm the service was stopped; manual inspection is required"
            raise Failure("Initial installation failed; " + stop_result
                          + ". Account/data and diagnostic files were retained; no successful installation was recorded."
                          + " Partial installations require administrator review, not automatic adoption.") from exc

    def execute(self, options, source, arch):
        p, f, r = self.paths, self.files, self.runtime
        with r.lock(p, f):
            recovered = self.recover_transaction(arch)
            if f.exists(p.config):
                old, old_raw, current = self.inspect(arch)
                if recovered:
                    print("Interrupted update recovered; v" + current["version"]
                          + " is healthy. No new release was attempted; run sudo panel-update again when ready.")
                    return
                if not options.update:
                    require(options.repo == old["repository"] and (options.origin is None or options.origin == old["origin"]),
                            "Existing installation uses different settings; it will not be overwritten.")
                    print("Already installed at v" + current["version"] + "; account, data and configuration preserved. Use sudo panel-update.")
                    return
                repo = old["repository"]
            else:
                require(not options.update, "No recognized installation exists; first run the reviewed local install.sh.")
                self.initial_check()
                repo, old, old_raw = options.repo, None, None
            tag, sizes = self.target(repo, options.version)
            if old is not None:
                comparison = version_tuple(tag, tagged=True)
                require(comparison >= version_tuple(old["version"]), "Downgrades are unsupported; no service changes were made.")
                if comparison == version_tuple(old["version"]):
                    print("Already running " + tag + "; no changes made.")
                    return
            action = "Update from v" + old["version"] if old else "Install"
            r.confirm(action + " to " + tag + " from " + repo + ". Fixed service: " + UNIT
                      + "; binary: /usr/local/bin/panel; private data: /var/lib/server-panel; loopback: " + LISTEN + ".", options.yes)
            with f.staging(p.binary.parent) as directory:
                candidate, metadata, digest = self.stage_release(directory, repo, tag, sizes, arch)
                normalized, host = origin(options.origin or "") if old is None else (old["origin"], old["healthHost"])
                config = {
                    "format": 1, "repository": repo, "listen": LISTEN, "origin": normalized, "healthHost": host,
                    "version": metadata["version"], "dataSchema": 1, "commit": metadata["commit"], "buildTime": metadata["buildTime"],
                    "binarySHA256": digest, "installerSHA256": old["installerSHA256"] if old else hashlib.sha256(source).hexdigest(),
                }
                if old is None:
                    self.apply_install(candidate, config, source)
                else:
                    self.apply_update(directory, candidate, old, old_raw, config)
                print("Healthy " + tag + " installed. No account/configuration data or .panel.lock was removed.")
                print("Manage future releases with sudo panel-update. Initial account setup is available through the configured panel origin.")


def arguments(argv):
    parser = argparse.ArgumentParser(description="Install a reviewed GitHub release on systemd Linux, or update the fixed installation.", allow_abbrev=False)
    parser.add_argument("--repo", help="public GitHub OWNER/REPO; required only for first installation")
    parser.add_argument("--version", help="stable release tag, e.g. v0.4.0; default: latest")
    parser.add_argument("--origin", help="HTTPS external domain; default: private loopback only")
    parser.add_argument("--yes", action="store_true", help="explicit unattended confirmation")
    parser.add_argument("--update", action="store_true", help=argparse.SUPPRESS)
    seen = set()
    for item in argv:
        if item.startswith("--"):
            name = item.split("=", 1)[0]
            require(name not in seen, "Duplicate command-line option.")
            seen.add(name)
    result = parser.parse_args(argv)
    if result.version is not None:
        version_tuple(result.version, tagged=True)
    if result.update:
        require(result.repo is None and result.origin is None, "Update settings come only from root-owned install.json; --repo/--origin are forbidden.")
    else:
        require(result.repo is not None, "First installation requires --repo OWNER/REPO.")
        result.repo = repository(result.repo)
        if result.origin is not None:
            result.origin = origin(result.origin)[0]
    return result


def main():
    options = arguments(sys.argv[2:])
    manager = Manager()
    arch = manager.runtime.preflight(manager.files)
    source = read_source(sys.argv[1], manager.paths.installer, manager.files, options.update)
    os.umask(0o077)

    def interrupted(_signum, _frame):
        raise Interrupted("Interrupted; restoring the previous version if service replacement had started.")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    manager.execute(options, source, arch)


if __name__ == "__main__":
    try:
        main()
    except (Failure, Interrupted, OSError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        print("panel installer: " + (str(error) if isinstance(error, (Failure, Interrupted)) else type(error).__name__ + "; no unsafe fallback was attempted."), file=sys.stderr)
        sys.exit(1)
PANEL_PYTHON
