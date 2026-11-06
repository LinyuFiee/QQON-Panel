#!/usr/bin/env python3
"""Build or verify the six fixed GitHub Release assets using only the stdlib.

Run with Python isolated mode (-I). Build output must be a new absolute path
whose parent already exists. Existing paths are never reused or removed; a
failed build may leave an incomplete directory for inspection, not publication.
"""

import argparse
import datetime
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
import subprocess
import tarfile
import tempfile
import zlib


PROJECT_ROOT = Path(__file__).resolve().parents[1]
ARCHITECTURES = ("amd64", "arm64")
SCRIPT_LIMITS = {"install.sh": 256 * 1024, "update.sh": 16 * 1024}
ASSET_NAMES = (
    "panel-linux-amd64.tar.gz", "panel-linux-arm64.tar.gz", "install.sh", "update.sh",
    "release.json", "SHA256SUMS",
)
MAX_BINARY_SIZE = 64 * 1024 * 1024
MAX_ARCHIVE_SIZE = 64 * 1024 * 1024
MAX_TAR_SIZE = MAX_BINARY_SIZE + 10240
MAX_MANIFEST_SIZE = 32 * 1024
MAX_CHECKSUMS_SIZE = 16 * 1024
COPY_BUFFER_SIZE = 64 * 1024
BUILD_TIMEOUT_SECONDS = 600


class ReleaseError(ValueError):
    """Invalid release input, unsafe path, failed build, or invalid artifact."""


def validate_version(tag):
    match = re.fullmatch(r"v(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})\.(0|[1-9][0-9]{0,9})", tag or "")
    if not match or any(int(part) > 2147483647 for part in match.groups()):
        raise ReleaseError("version must be stable vMAJOR.MINOR.PATCH; components must be <= 2147483647 without leading zeros")
    return tag[1:]


def validate_repository(repository):
    if not isinstance(repository, str) or not re.fullmatch(
        r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,98}[A-Za-z0-9])?/"
        r"[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,98}[A-Za-z0-9])?", repository,
    ):
        raise ReleaseError("repository must be owner/repo, 1-100 ASCII characters per segment, with alphanumeric ends; owner allows '-', repo allows '._-'")
    return repository


def validate_commit(commit):
    if not isinstance(commit, str) or not re.fullmatch(r"(?:[0-9a-f]{40}|[0-9a-f]{64})", commit):
        raise ReleaseError("commit must be an explicit full 40- or 64-character lowercase hexadecimal Git commit")
    return commit


def validate_build_time(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value):
        raise ReleaseError("build time must be UTC YYYY-MM-DDTHH:MM:SSZ")
    try:
        datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")
    except ValueError as error:
        raise ReleaseError("build time must be a real UTC calendar date and time") from error
    return value


def _identity(version, repository, commit, build_time):
    return {
        "format": 2,
        "version": validate_version(version),
        "tag": version,
        "repository": validate_repository(repository),
        "dataSchema": 1,
        "commit": validate_commit(commit),
        "buildTime": validate_build_time(build_time),
    }


def _absolute_path(value):
    path = Path(value)
    if not path.is_absolute() or ".." in path.parts:
        raise ReleaseError("paths must be absolute and must not contain '..'")
    return path


def _real_path(path, *, directory):
    """Reject symlinks and Windows junctions/reparse points in every component."""
    path = _absolute_path(path)
    for component in (*reversed(path.parents), path):
        try:
            info = component.lstat()
        except OSError as error:
            raise ReleaseError(f"required path is unavailable: {component}") from error
        if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
            raise ReleaseError(f"symlinks and reparse points are not allowed: {component}")
        expects_directory = component != path or directory
        if not (stat.S_ISDIR(info.st_mode) if expects_directory else stat.S_ISREG(info.st_mode)):
            raise ReleaseError(f"expected a real {'directory' if expects_directory else 'regular file'}: {component}")
    return info


def _new_output_path(value):
    path = _absolute_path(value)
    _real_path(path.parent, directory=True)
    if os.path.lexists(path):
        raise ReleaseError(f"output already exists; choose a new path: {path}")
    return path


def _read_bounded(path, limit):
    info = _real_path(path, directory=False)
    if not 0 < info.st_size <= limit:
        raise ReleaseError(f"file must contain 1-{limit} bytes: {path.name}")
    with path.open("rb") as stream:
        data = stream.read(limit + 1)
    if not 0 < len(data) <= limit or len(data) != info.st_size:
        raise ReleaseError(f"file changed or exceeded its size limit: {path.name}")
    return data


def _file_digest(path, limit):
    info = _real_path(path, directory=False)
    if not 0 < info.st_size <= limit:
        raise ReleaseError(f"file must contain 1-{limit} bytes: {path.name}")
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as stream:
        while True:
            chunk = stream.read(64 * 1024)
            if not chunk:
                break
            size += len(chunk)
            if size > limit:
                raise ReleaseError(f"file exceeded its size limit: {path.name}")
            digest.update(chunk)
    if size != info.st_size:
        raise ReleaseError(f"file changed while being hashed: {path.name}")
    return size, digest.hexdigest()


def _write_new(path, data, *, executable=False):
    with path.open("xb") as stream:
        stream.write(data)
    path.chmod(0o755 if executable else 0o644)


def _check_static_elf(path, arch, size):
    with path.open("rb") as stream:
        header = stream.read(64)
        if len(header) != 64:
            raise ReleaseError(f"truncated ELF header: {path.name}")
        fields = struct.unpack("<16sHHIQQQIHHHHHH", header)
        ident, kind, machine, elf_version = fields[:4]
        phoff, ehsize, phentsize, phnum = fields[5], fields[8], fields[9], fields[10]
        if (ident[:7] != b"\x7fELF\x02\x01\x01" or kind != 2 or elf_version != 1
                or machine != {"amd64": 62, "arm64": 183}[arch]
                or ehsize != 64 or phentsize != 56 or not 1 <= phnum <= 128
                or phoff < 64 or phoff + phentsize * phnum > size):
            raise ReleaseError(f"expected a static 64-bit little-endian Linux {arch} ELF executable: {path.name}")
        stream.seek(phoff)
        program_headers = stream.read(phentsize * phnum)
        if len(program_headers) != phentsize * phnum:
            raise ReleaseError(f"truncated ELF program headers: {path.name}")
        types = [struct.unpack_from("<I", program_headers, index * phentsize)[0] for index in range(phnum)]
        if 1 not in types or any(kind in (2, 3) for kind in types):
            raise ReleaseError(f"ELF must have load segments and no dynamic section or interpreter: {path.name}")


def _binary_record(path, arch):
    size, sha256 = _file_digest(path, MAX_BINARY_SIZE)
    _check_static_elf(path, arch, size)
    return {"os": "linux", "arch": arch, "name": "panel", "size": size, "sha256": sha256}


def _panel_header(size):
    # A plain, normalized USTAR header: no GNU/PAX extensions or path prefixes.
    member = tarfile.TarInfo("panel")
    member.type = tarfile.REGTYPE
    member.size = size
    member.mode = 0o755
    member.uid = member.gid = member.mtime = 0
    member.uname = member.gname = member.linkname = ""
    return member.tobuf(format=tarfile.USTAR_FORMAT)


def _copy_exact(source, destination, size, *, eof=False):
    """Never copy beyond the declared size, even if a source grows mid-copy."""
    digest = hashlib.sha256()
    remaining = size
    while remaining:
        chunk = source.read(min(COPY_BUFFER_SIZE, remaining))
        if not chunk:
            raise ReleaseError("truncated binary while copying")
        destination.write(chunk)
        digest.update(chunk)
        remaining -= len(chunk)
    if eof and source.read(1):
        raise ReleaseError("binary grew beyond its declared size while copying")
    return digest.hexdigest()


class _LimitedWriter:
    def __init__(self, stream, limit):
        self.stream, self.limit, self.size = stream, limit, 0

    def write(self, data):
        if self.size + len(data) > self.limit:
            raise ReleaseError("compressed archive exceeds its size limit")
        self.size += len(data)
        return self.stream.write(data)


def _archive_binary(binary, record, output):
    name = f"panel-linux-{record['arch']}.tar.gz"
    archive = output / name
    size = record["size"]
    _real_path(binary, directory=False)
    with archive.open("xb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=_LimitedWriter(raw, MAX_ARCHIVE_SIZE),
                           compresslevel=9, mtime=0) as compressed:
            compressed.write(_panel_header(size))
            with binary.open("rb") as source:
                if _copy_exact(source, compressed, size, eof=True) != record["sha256"]:
                    raise ReleaseError("binary changed while being archived")
            padding = (-size) % tarfile.BLOCKSIZE
            ending = padding + 2 * tarfile.BLOCKSIZE
            ending += (-(tarfile.BLOCKSIZE + size + ending)) % tarfile.RECORDSIZE
            compressed.write(b"\0" * ending)
    archive.chmod(0o644)
    archive_size, digest = _file_digest(archive, MAX_ARCHIVE_SIZE)
    return {"name": name, "size": archive_size, "sha256": digest}


def _validate_binary_record(record, arch):
    fields = {"os", "arch", "name", "size", "sha256", "archive"}
    if (type(record) is not dict or set(record) != fields or arch not in ARCHITECTURES
            or record["os"] != "linux" or record["arch"] != arch or record["name"] != "panel"):
        raise ReleaseError("invalid format 2 binary identity or fields")
    archive = record["archive"]
    if (type(archive) is not dict or set(archive) != {"name", "size", "sha256"}
            or archive["name"] != f"panel-linux-{arch}.tar.gz"):
        raise ReleaseError("invalid format 2 archive identity or fields")
    for value, limit in ((record, MAX_BINARY_SIZE), (archive, MAX_ARCHIVE_SIZE)):
        if type(value["size"]) is not int or not 0 < value["size"] <= limit:
            raise ReleaseError("invalid binary or archive size")
        if type(value["sha256"]) is not str or not re.fullmatch(r"[0-9a-f]{64}", value["sha256"]):
            raise ReleaseError("invalid binary or archive SHA-256")


def _gunzip_bounded(source, destination, expected):
    """Decode one gzip member, bounding every expansion before writing/allocating."""
    decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
    digest = hashlib.sha256()
    input_size = output_size = 0
    try:
        while True:
            chunk = source.read(COPY_BUFFER_SIZE)
            if not chunk:
                break
            input_size += len(chunk)
            if input_size > expected["size"] or input_size > MAX_ARCHIVE_SIZE:
                raise ReleaseError("archive grew beyond its size limit")
            digest.update(chunk)
            if decoder.eof:
                raise ReleaseError("extra gzip member or trailing compressed data")
            pending = chunk
            while True:
                allowance = min(COPY_BUFFER_SIZE, MAX_TAR_SIZE - output_size + 1)
                data = decoder.decompress(pending, allowance)
                output_size += len(data)
                if output_size > MAX_TAR_SIZE:
                    raise ReleaseError("uncompressed tar exceeds its size limit")
                destination.write(data)
                pending = decoder.unconsumed_tail
                if decoder.unused_data:
                    raise ReleaseError("extra gzip member or trailing compressed data")
                if decoder.eof or (not pending and len(data) < allowance):
                    break
    except zlib.error as error:
        raise ReleaseError("invalid gzip integrity or compressed data") from error
    if not decoder.eof:
        raise ReleaseError("truncated gzip archive")
    if input_size != expected["size"] or digest.hexdigest() != expected["sha256"]:
        raise ReleaseError("archive changed while being decompressed")
    if output_size % tarfile.BLOCKSIZE:
        raise ReleaseError("truncated tar block")
    destination.seek(0)


def extract_archive(archive, record, destination):
    """Verify and extract only a fixed panel file; never execute archive contents.

    Callers must supply an unused panel path in a private real directory. The
    compressed hash is checked before decompression and again during the bounded
    copy. Only our own exclusively-created file is removed on a failed check.
    """
    _validate_binary_record(record, record.get("arch") if isinstance(record, dict) else None)
    archive = _absolute_path(archive)
    destination = _absolute_path(destination)
    _real_path(destination.parent, directory=True)
    if destination.name != "panel" or os.path.lexists(destination):
        raise ReleaseError("extraction requires a new fixed panel destination")
    expected = record["archive"]
    if archive.name != expected["name"] or _file_digest(archive, MAX_ARCHIVE_SIZE) != (expected["size"], expected["sha256"]):
        raise ReleaseError("archive size or SHA-256 mismatch")
    created = False
    try:
        # The temporary tar is outside the release/download directory. A bomb
        # cannot grow it past MAX_TAR_SIZE, nor allocate a whole expanded tar.
        with tempfile.TemporaryFile(mode="w+b") as unpacked:
            with archive.open("rb") as source:
                _gunzip_bounded(source, unpacked, expected)
            if unpacked.read(tarfile.BLOCKSIZE) != _panel_header(record["size"]):
                raise ReleaseError("archive must contain one normalized USTAR regular panel file")
            with destination.open("xb") as binary:
                created = True
                digest = _copy_exact(unpacked, binary, record["size"])
            padding = (-record["size"]) % tarfile.BLOCKSIZE
            zeros = unpacked.read(padding + 2 * tarfile.BLOCKSIZE)
            if len(zeros) != padding + 2 * tarfile.BLOCKSIZE or any(zeros):
                raise ReleaseError("truncated tar ending, extra member, or nonzero padding")
            while True:
                zeros = unpacked.read(COPY_BUFFER_SIZE)
                if not zeros:
                    break
                if any(zeros):
                    raise ReleaseError("extra tar member or trailing nonzero data")
            if digest != record["sha256"]:
                raise ReleaseError("inner ELF SHA-256 mismatch")
        _check_static_elf(destination, record["arch"], record["size"])
        destination.chmod(0o755)
        return destination
    except BaseException:
        if created:
            destination.unlink()
        raise


def _asset_limit(name):
    if name in SCRIPT_LIMITS:
        return SCRIPT_LIMITS[name]
    if name == "release.json":
        return MAX_MANIFEST_SIZE
    if name == "SHA256SUMS":
        return MAX_CHECKSUMS_SIZE
    return MAX_ARCHIVE_SIZE


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ReleaseError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _reject_constant(value):
    raise ReleaseError(f"non-JSON numeric constant: {value}")


def verify_release(*, version, repository, commit, output, build_time=None):
    """Verify the exact asset set and identity before the publication job uploads."""
    validate_version(version)
    validate_repository(repository)
    validate_commit(commit)
    if build_time is not None:
        validate_build_time(build_time)
    output = _absolute_path(output)
    _real_path(output, directory=True)
    if {path.name for path in output.iterdir()} != set(ASSET_NAMES):
        raise ReleaseError("output must contain exactly the six contract assets")
    checksums = _read_bounded(output / "SHA256SUMS", MAX_CHECKSUMS_SIZE)
    try:
        lines = checksums.decode("ascii").splitlines(keepends=True)
    except UnicodeError as error:
        raise ReleaseError("SHA256SUMS must be ASCII") from error
    seen = set()
    for line in lines:
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9.-]+)\n", line)
        if not match:
            raise ReleaseError("SHA256SUMS must use lowercase hex, two spaces and LF line endings")
        expected_hash, name = match.groups()
        if name not in ASSET_NAMES[:-1] or name in seen:
            raise ReleaseError("SHA256SUMS has an unexpected or duplicate filename")
        seen.add(name)
        if _file_digest(output / name, _asset_limit(name))[1] != expected_hash:
            raise ReleaseError(f"SHA256SUMS mismatch: {name}")
    if seen != set(ASSET_NAMES[:-1]):
        raise ReleaseError("SHA256SUMS must cover every asset except itself")
    try:
        manifest = json.loads(
            _read_bounded(output / "release.json", MAX_MANIFEST_SIZE).decode("utf-8"),
            object_pairs_hook=_unique_object, parse_constant=_reject_constant,
        )
    except (UnicodeError, json.JSONDecodeError, RecursionError) as error:
        raise ReleaseError("release.json must be bounded, valid UTF-8 JSON") from error
    if not isinstance(manifest, dict):
        raise ReleaseError("release.json must be an object")
    expected = _identity(version, repository, commit, manifest.get("buildTime") if build_time is None else build_time)
    if set(manifest) != set(expected) | {"binaries"}:
        raise ReleaseError("release.json has missing or extra fields")
    for field, value in expected.items():
        if type(manifest[field]) is not type(value) or manifest[field] != value:
            raise ReleaseError(f"release.json does not match expected {field}")
    binaries = manifest["binaries"]
    if not isinstance(binaries, list) or len(binaries) != len(ARCHITECTURES):
        raise ReleaseError("release.json must describe exactly amd64 and arm64 binaries")
    for record, arch in zip(binaries, ARCHITECTURES):
        _validate_binary_record(record, arch)
        with tempfile.TemporaryDirectory(prefix="panel-verify-") as temporary:
            extract_archive(output / record["archive"]["name"], record, Path(temporary) / "panel")
    return manifest


def build_release(*, version, repository, commit, output, build_time=None, go="go", project_root=PROJECT_ROOT):
    """Build from trusted project source; copy only the two reviewed local scripts."""
    if build_time is None:
        build_time = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
    manifest = _identity(version, repository, commit, build_time)
    output = _new_output_path(output)
    project_root = _absolute_path(project_root)
    _real_path(project_root, directory=True)
    scripts = {
        name: _read_bounded(project_root / "scripts" / name, limit)
        for name, limit in SCRIPT_LIMITS.items()
    }
    try:
        output.mkdir(mode=0o700)
        ldflags = " ".join((
            "-s -w", "-X main.version=" + manifest["version"], "-X main.commit=" + manifest["commit"],
            "-X main.buildTime=" + manifest["buildTime"], "-X main.githubRepository=" + manifest["repository"],
        ))
        manifest["binaries"] = []
        with tempfile.TemporaryDirectory(prefix="panel-build-") as temporary:
            for arch in ARCHITECTURES:
                env = os.environ.copy()
                env.update({
                    "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch,
                    "GOFLAGS": "", "GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local",
                    "GOAMD64": "v1", "GOARM64": "v8.0", "GOEXPERIMENT": "",
                })
                binary = Path(temporary) / f"panel-linux-{arch}"
                command = [str(go), "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", str(binary), "."]
                subprocess.run(command, cwd=project_root, env=env, check=True, shell=False, timeout=BUILD_TIMEOUT_SECONDS)
                record = _binary_record(binary, arch)
                record["archive"] = _archive_binary(binary, record, output)
                manifest["binaries"].append(record)
        for name, data in scripts.items():
            _write_new(output / name, data, executable=True)
        metadata = (json.dumps(manifest, indent=2, ensure_ascii=True, allow_nan=False) + "\n").encode("utf-8")
        if len(metadata) > MAX_MANIFEST_SIZE:
            raise ReleaseError("generated release.json exceeds its size limit")
        _write_new(output / "release.json", metadata)
        checksums = "".join(
            f"{_file_digest(output / name, _asset_limit(name))[1]}  {name}\n"
            for name in ASSET_NAMES[:-1]
        ).encode("ascii")
        if len(checksums) > MAX_CHECKSUMS_SIZE:
            raise ReleaseError("generated SHA256SUMS exceeds its size limit")
        _write_new(output / "SHA256SUMS", checksums)
        return verify_release(version=version, repository=repository, commit=commit, build_time=build_time, output=output)
    except (OSError, subprocess.SubprocessError) as error:
        raise ReleaseError(f"release build failed; do not publish incomplete output at {output}: {error}") from error


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="stable tag vMAJOR.MINOR.PATCH")
    parser.add_argument("--repo", required=True, help="configured GitHub owner/repo")
    parser.add_argument("--commit", required=True, help="full lowercase Git SHA (40 or 64 hex characters)")
    parser.add_argument("--build-time", help="UTC YYYY-MM-DDTHH:MM:SSZ; defaults to current UTC when building")
    parser.add_argument("--go", default="go", help="Go executable; use the contract toolchain path for local builds")
    parser.add_argument("--output", required=True, help="absolute new output directory with an existing parent")
    parser.add_argument("--verify-only", action="store_true", help="verify an existing asset directory without building or modifying it")
    arguments = parser.parse_args(argv)
    parameters = dict(version=arguments.version, repository=arguments.repo, commit=arguments.commit,
                      build_time=arguments.build_time, output=arguments.output)
    try:
        if arguments.verify_only:
            verify_release(**parameters)
        else:
            build_release(**parameters, go=arguments.go)
    except (ReleaseError, OSError) as error:
        parser.exit(2, f"error: {error}\n")
    print(f"{'Verified' if arguments.verify_only else 'Built and verified'} {arguments.version}: {arguments.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
