import base64
import hashlib
import io
import stat
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile
import sys

sys.path.insert(0, str(Path(__file__).parents[1] / 'macos'))
import sign_development
import signing


class DevelopmentIdentity(unittest.TestCase):
    def test_matches_team_by_certificate_ou_not_the_team_member_in_common_name(self):
        certificate = b'test certificate DER'
        fingerprint = hashlib.sha1(certificate).hexdigest().upper()
        pem = '-----BEGIN CERTIFICATE-----\n' + base64.b64encode(certificate).decode() + '\n-----END CERTIFICATE-----\n'
        identities = f'1) {fingerprint} "Apple Development: Example (MEMBER1234)"\n'
        subject = b'subject=\n    CN=Apple Development: Example (MEMBER1234)\n    OU=TEAMID1234\n'
        for requested, expected in ((None, (fingerprint, 'TEAMID1234')), ('TEAMID1234', (fingerprint, 'TEAMID1234'))):
            with self.subTest(requested=requested), patch.object(signing, 'private_run', side_effect=[
                    SimpleNamespace(stdout=identities), SimpleNamespace(stdout=pem)]), patch.object(
                    signing.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout=subject)):
                self.assertEqual(signing.development_identity(requested), expected)
        with patch.object(signing, 'private_run', side_effect=[SimpleNamespace(stdout=identities), SimpleNamespace(stdout=pem)]), patch.object(
                signing.subprocess, 'run', return_value=SimpleNamespace(returncode=0, stdout=subject)):
            with self.assertRaisesRegex(RuntimeError, 'Expected one valid'):
                signing.development_identity('MEMBER1234')


class ArchiveExtraction(unittest.TestCase):
    def archive(self, *entries):
        output = io.BytesIO()
        with zipfile.ZipFile(output, 'w') as archive:
            for name, mode, contents in entries:
                item = zipfile.ZipInfo(name)
                item.create_system = 3
                item.external_attr = mode << 16
                archive.writestr(item, contents)
        return output

    def test_rejects_escaping_paths_symlinks_and_case_collisions(self):
        regular = stat.S_IFREG | 0o644
        for entries in (
                (('Oink.app/../../elsewhere', regular, b''),),
                (('Oink.app/absolute', stat.S_IFLNK | 0o777, b'/tmp'),),
                (('Another.app/Contents/file', regular, b''),),
                (('Oink.app/Contents/file', regular, b''), ('Oink.app/contents/FILE', regular, b''))):
            with self.subTest(entries=entries), zipfile.ZipFile(self.archive(*entries)) as archive:
                with self.assertRaises(RuntimeError):
                    sign_development.archive_entries(archive, 'Oink.app')

    def test_preserves_executable_mode(self):
        mode = stat.S_IFREG | 0o755
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'Oink.app/Contents/MacOS/oink-backend'
            archive = self.archive(('Oink.app/Contents/MacOS/oink-backend', mode, b'executable fixture'))
            sign_development.extract_archive(archive, Path(temp), 'Oink.app')
            self.assertEqual(path.read_bytes(), b'executable fixture')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o755)

    def test_digest_mismatch_leaves_output_and_signing_identity_unused(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(sign_development, 'sign_app') as sign:
            root = Path(temp)
            archive = root / 'staging.zip'
            archive.write_bytes(b'untrusted archive')
            target = root / 'Oink.app'
            with self.assertRaisesRegex(RuntimeError, 'SHA-256 differs'):
                sign_development.sign_archive(archive, '0' * 64, target, 'A' * 40, 'TEAMID1234')
            self.assertFalse(target.exists())
            sign.assert_not_called()
