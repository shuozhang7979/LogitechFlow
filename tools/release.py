"""Build DeskSpan kits with a pinned, verifiable external runtime (Python 3.9+)."""

import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from urllib.request import Request, urlopen
from zipfile import ZIP_DEFLATED, ZIP_STORED, ZipFile, ZipInfo


ROOT = Path(__file__).resolve().parents[1]
DIST = ROOT / "dist"
NAME = "DeskSpan-0.1.0"
SOURCE_NAME = NAME + "-source.zip"
OINK = "0.3.6"
BASE_URL = f"https://github.com/oinkvm/oink/releases/download/v{OINK}/"
SOURCE_URL = f"https://codeload.github.com/oinkvm/oink/zip/refs/tags/v{OINK}"
UPSTREAM = ROOT / f"oink-v{OINK}.zip"
UPSTREAM_SHA = "8b8db86063856fc3aad2367338163da5f165fb592ac7709be8be7d96a754b2ed"
# Published at https://github.com/oinkvm/oink/releases/expanded_assets/v0.3.6
TARGETS = {
    "windows-x64": ("windows", "amd64", "msi", "101293d71f7f9be1e3edc6a0f63219512e22a0449ea16737e9db3a060aecf9f7"),
    "macos-arm64": ("darwin", "arm64", "dmg", "7966aba744e51d2c6a25f36ddc94ea3a52eec9fd7386ca73465502b433f42cdb"),
    "macos-x64": ("darwin", "amd64", "dmg", "789dc3c227bce9e06cf84d8290743cab3e03ecdb22d21235159048a89cde440a"),
}
DOCS = ("README.md", "LICENSE", "LICENSE-ADDITIONAL-TERMS.md", "NOTICE")


def digest(path):
    with path.open("rb") as stream:
        result = hashlib.sha256()
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
        return result.hexdigest()


def verified(path, expected):
    if digest(path) != expected:
        raise ValueError(f"SHA-256 mismatch: {path}")


def fetch_file(url, path, expected):
    if path.exists():
        verified(path, expected)
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    request = Request(url, headers={"User-Agent": NAME})
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as temp:
        pending = Path(temp.name)
    try:
        with pending.open("wb") as out, urlopen(request, timeout=120) as incoming:
            shutil.copyfileobj(incoming, out)
        verified(pending, expected)
        os.replace(pending, path)
    finally:
        pending.unlink(missing_ok=True)


def installer_name(target):
    return f"Oink-{OINK}-{target}.{TARGETS[target][2]}"


def fetch():
    fetch_file(SOURCE_URL, UPSTREAM, UPSTREAM_SHA)
    for target, (_, _, _, expected) in TARGETS.items():
        filename = installer_name(target)
        fetch_file(BASE_URL + filename, DIST / "installers" / filename, expected)
        print(f"Verified {filename}")


def entry(archive, name, data, mode=0o644):
    # Fix timestamps, modes and member order for stable output.
    info = ZipInfo(name, date_time=(2026, 11, 18, 0, 0, 0))
    info.create_system = 3
    info.external_attr = (0o100000 | mode) << 16
    info.compress_type = ZIP_STORED if name.endswith(".zip") else ZIP_DEFLATED
    archive.writestr(info, data)


def build_source(output):
    files = [ROOT / item for item in (*DOCS, "go.mod", ".gitignore", ".gitattributes")]
    files += sorted((ROOT / "cmd" / "deskspan").rglob("*.go"))
    files += [ROOT / "tools" / name for name in (
        "release.py", "setup-windows.cmd.in", "setup-macos.command.in",
    )]
    files.append(UPSTREAM)
    with ZipFile(output, "w") as archive:
        for path in files:
            entry(archive, path.relative_to(ROOT).as_posix(), path.read_bytes())


def render_setup(target):
    filename = installer_name(target)
    windows = target == "windows-x64"
    template = "setup-windows.cmd.in" if windows else "setup-macos.command.in"
    text = (ROOT / "tools" / template).read_text(encoding="utf-8")
    for key, value in (("NAME", filename), ("SHA256", TARGETS[target][3]), ("URL", BASE_URL + filename)):
        text = text.replace("@" + key + "@", value)
    if windows:
        text = text.replace("\n", "\r\n")
    return text.encode("utf-8")


def layout_launcher(windows):
    if windows:
        return ("@echo off\r\nsetlocal\r\nchcp 65001 >nul\r\n"
                '"%~dp0deskspan.exe" setup\r\nset "code=%ERRORLEVEL%"\r\npause\r\nexit /b %code%\r\n').encode()
    return ('#!/bin/sh\ncd "$(dirname "$0")" || exit 1\n./deskspan setup\n'
            'code=$?\nprintf "\\n按回车关闭窗口…"\nread -r close\nexit "$code"\n').encode()


def kit_name(target):
    return f"{NAME}-{target}.zip"


def build_kit(staging, target, go_license):
    goos, goarch, _, _ = TARGETS[target]
    windows = goos == "windows"
    executable = "deskspan.exe" if windows else "deskspan"
    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0",
               GOTOOLCHAIN="local", GOWORK="off", GOPROXY="off",
               GOCACHE=str(DIST / "cache" / "go-build"))
    binary = staging / executable
    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-mod=readonly",
                    "-o", str(binary), "./cmd/deskspan"], cwd=ROOT, env=env, check=True)
    with ZipFile(staging / kit_name(target), "w") as archive:
        entry(archive, executable, binary.read_bytes(), 0o755)
        entry(archive, "安装运行时.cmd" if windows else "安装运行时.command",
              render_setup(target), 0o755)
        entry(archive, "配置多屏.cmd" if windows else "配置多屏.command",
              layout_launcher(windows), 0o755)
        for name in DOCS:
            entry(archive, name, (ROOT / name).read_bytes())
        entry(archive, "Go-LICENSE.txt", go_license)
        entry(archive, SOURCE_NAME, (staging / SOURCE_NAME).read_bytes())


def expected_names():
    return [SOURCE_NAME] + [kit_name(target) for target in TARGETS]


def build():
    DIST.mkdir(exist_ok=True)
    fetch_file(SOURCE_URL, UPSTREAM, UPSTREAM_SHA)
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    with tempfile.TemporaryDirectory(dir=DIST) as temp:
        staging = Path(temp)
        build_source(staging / SOURCE_NAME)
        for target in TARGETS:
            build_kit(staging, target, (goroot / "LICENSE").read_bytes())
        names = expected_names()
        sums = "".join(f"{digest(staging / name)}  {name}\n" for name in names)
        (staging / "SHA256SUMS").write_bytes(sums.encode("ascii"))
        for name in names + ["SHA256SUMS"]:
            os.replace(staging / name, DIST / name)
    verify()


def verify():
    expected = {}
    for line in (DIST / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        checksum, name = line.split("  ")
        if name in expected:
            raise ValueError(f"Duplicate archive in SHA256SUMS: {name}")
        expected[name] = checksum
    if set(expected) != set(expected_names()):
        raise ValueError("Unexpected release file list")
    for name, checksum in expected.items():
        path = DIST / name
        verified(path, checksum)
        with ZipFile(path) as archive:
            bad = archive.testzip()
            if bad:
                raise ValueError(f"Invalid CRC: {name}/{bad}")
            if name == SOURCE_NAME:
                inner = archive.read(UPSTREAM.name)
                required = {*DOCS, "go.mod", "tools/release.py", "cmd/deskspan/main.go"}
                if not required.issubset(archive.namelist()):
                    raise ValueError("Missing source files")
                wanted = UPSTREAM_SHA
            else:
                windows = name == kit_name("windows-x64")
                extension = "cmd" if windows else "command"
                executable = "deskspan.exe" if windows else "deskspan"
                for launch in (f"安装运行时.{extension}", f"配置多屏.{extension}", executable):
                    info = archive.getinfo(launch)
                    if info.external_attr >> 16 & 0o111 == 0:
                        raise ValueError(f"Lost executable mode on {launch}")
                inner = archive.read(SOURCE_NAME)
                wanted = expected[SOURCE_NAME]
            if hashlib.sha256(inner).hexdigest() != wanted:
                raise ValueError(f"Unexpected embedded source in {name}")
        print(f"Verified {name}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("build", "fetch", "verify"))
    {"build": build, "fetch": fetch, "verify": verify}[parser.parse_args().action]()
