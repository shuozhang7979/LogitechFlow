#!/usr/bin/env python3
"""Sign an Oink development staging ZIP with a Mac's Apple Development identity."""
import argparse
import json
from pathlib import Path
import re
import shutil
import stat
import sys
import tempfile
import unicodedata
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'build-support'))
from identity import IDENTITY
from provenance import digest
from content import verify_app
from signing import awake_for_signing, development_identity, sign_app


def archive_entries(archive, application):
    entries = []
    seen = set()
    for entry in archive.infolist():
        parts = entry.filename.removesuffix('/').split('/')
        mode = entry.external_attr >> 16
        expected_type = stat.S_IFDIR if entry.is_dir() else stat.S_IFREG
        if (entry.orig_filename != entry.filename or parts[0] != application
                or any(part in ('', '.', '..') or '\\' in part for part in parts)
                or stat.S_IFMT(mode) not in (0, expected_type) or mode & 0o7000
                or len(parts) == 1 and not entry.is_dir()):
            raise RuntimeError('Staging archive contains an unsafe path or file type')
        normalized = tuple(unicodedata.normalize('NFD', part).casefold() for part in parts)
        if normalized in seen:
            raise RuntimeError('Staging archive contains conflicting paths')
        seen.add(normalized)
        entries.append((entry, parts, mode))
    if not entries:
        raise RuntimeError('Staging archive is empty')
    return entries


def extract_archive(archive, destination, application):
    with zipfile.ZipFile(archive) as source:
        for entry, parts, mode in archive_entries(source, application):
            path = destination.joinpath(*parts)
            if entry.is_dir():
                path.mkdir(parents=True, exist_ok=True)
                continue
            path.parent.mkdir(parents=True, exist_ok=True)
            with source.open(entry) as contents, path.open('xb') as output:
                shutil.copyfileobj(contents, output)
            path.chmod(mode & 0o777 or 0o644)


def sign_archive(archive, expected_digest, target, certificate, team):
    identity = {**IDENTITY, 'macos': {**IDENTITY['macos'], 'team_id': team}}
    application = identity['macos']['app']
    with tempfile.TemporaryDirectory(prefix='.oink-signing-', dir=target.parent) as temporary:
        temporary = Path(temporary)
        copied_archive = Path(shutil.copyfile(archive, temporary / 'staging.zip'))
        if digest(copied_archive) != expected_digest.lower():
            raise RuntimeError('Staging archive SHA-256 differs from the trusted GitHub build')
        extract_archive(copied_archive, temporary, application)
        app = temporary / application
        receipt = app / 'Contents/Resources' / identity['metadata_file']
        metadata = json.loads(receipt.read_text())
        architecture = {'arm64': 'arm64', 'x64': 'x86_64'}.get(metadata.get('architecture'))
        if architecture is None:
            raise RuntimeError('Staging archive declares an unsupported Mac architecture')
        verify_app(app, identity, architecture, adhoc=True, development=True)
        metadata['signing'] = 'apple-development'
        receipt.write_text(json.dumps(metadata, indent=2) + '\n')
        with awake_for_signing():
            group = f"{team}.{identity['macos']['app_group_suffix']}"
            sign_app(app, identity, group, certificate)
            verify_app(app, identity, architecture, development=True)
        if target.exists() or target.is_symlink():
            raise RuntimeError(f'Signed app output already exists: {target}')
        app.rename(target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--print-team-id', action='store_true', help='Read the actual team ID from an installed Apple Development certificate')
    parser.add_argument('--archive', type=Path, help='Oink development staging ZIP from a trusted GitHub Actions run')
    parser.add_argument('--sha256', help='Expected SHA-256 from the GitHub Actions build')
    parser.add_argument('--team', help='Apple Developer Team ID used for staging')
    parser.add_argument('--output', type=Path, help='Directory in which to create a signed Oink.app')
    parser.add_argument('--signing-fingerprint', help='Signing certificate SHA1 to select when more than one is available')
    args = parser.parse_args()
    if args.print_team_id:
        if args.archive or args.sha256 or args.team or args.output:
            parser.error('--print-team-id only accepts an optional signing fingerprint')
    elif not (args.archive and args.output and re.fullmatch(r'[0-9a-fA-F]{64}', args.sha256 or '')
              and re.fullmatch(r'[A-Z0-9]{10}', args.team or '')):
        parser.error('signing requires --archive, --sha256, --team, and --output')
    if sys.platform != 'darwin':
        parser.error('run the Apple Development signer on a Mac')
    try:
        certificate, team = development_identity(args.team, args.signing_fingerprint)
        if args.print_team_id:
            print(team)
            return
        output = args.output.resolve()
        target = output / IDENTITY['macos']['app']
        if target.exists() or target.is_symlink():
            raise RuntimeError(f'Signed app output already exists: {target}')
        output.mkdir(parents=True, exist_ok=True)
        sign_archive(args.archive, args.sha256, target, certificate, team)
        print(f'Signed and verified: {target}')
    except (OSError, RuntimeError, ValueError, zipfile.BadZipFile) as error:
        parser.exit(1, f'{error}\n')


if __name__ == '__main__':
    main()
