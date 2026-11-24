#!/usr/bin/env python3
"""Generate and Ed25519-sign Oink v2 (and Windows v1) update catalogs for GitHub Releases."""
import argparse
import base64
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import time

# Pure-Python RFC 8032 Ed25519 (zero external dependencies; matches ed25519-dalek verify_strict)
_P = 2**255 - 19
_L = 2**252 + 27742317777372353535851937790883648493
_D = -121665 * pow(121666, _P - 2, _P) % _P
_I = pow(2, (_P - 1) // 4, _P)


def _inv(x: int) -> int:
    return pow(x, _P - 2, _P)


def _xrecover(y: int) -> int:
    xx = (y * y - 1) * _inv(_D * y * y + 1) % _P
    x = pow(xx, (_P + 3) // 8, _P)
    if (x * x - xx) % _P != 0:
        x = (x * _I) % _P
    if x % 2 != 0:
        x = _P - x
    return x


_BY = 4 * _inv(5) % _P
_BX = _xrecover(_BY)
_B = (_BX % _P, _BY % _P, 1, (_BX * _BY) % _P)


def _edwards_add(p1, p2):
    x1, y1, z1, t1 = p1
    x2, y2, z2, t2 = p2
    a = (y1 - x1) * (y2 - x2) % _P
    b = (y1 + x1) * (y2 + x2) % _P
    c = t1 * 2 * _D * t2 % _P
    d = z1 * 2 * z2 % _P
    e = b - a
    f = d - c
    g = d + c
    h = b + a
    return (e * f % _P, g * h % _P, f * g % _P, e * h % _P)


def _scalarmult(pt, e: int):
    res = (0, 1, 1, 0)
    cur = pt
    while e > 0:
        if e & 1:
            res = _edwards_add(res, cur)
        cur = _edwards_add(cur, cur)
        e >>= 1
    return res


def _encodepoint(pt) -> bytes:
    x, y, z, _ = pt
    zi = _inv(z)
    x = (x * zi) % _P
    y = (y * zi) % _P
    return (y | ((x & 1) << 255)).to_bytes(32, 'little')


def _decodepoint(s: bytes):
    if len(s) != 32:
        raise ValueError('invalid point length')
    n = int.from_bytes(s, 'little')
    y = n & ((1 << 255) - 1)
    x_sign = n >> 255
    if y >= _P:
        raise ValueError('non-canonical y')
    x = _xrecover(y)
    if (x & 1) != x_sign:
        if x == 0:
            raise ValueError('invalid negative zero')
        x = _P - x
    return (x, y, 1, (x * y) % _P)


def ed25519_public_key(seed: bytes) -> bytes:
    if len(seed) != 32:
        raise ValueError('Ed25519 seed must be 32 bytes')
    h = hashlib.sha512(seed).digest()
    a = int.from_bytes(h[:32], 'little')
    a &= (1 << 254) - 8
    a |= 1 << 254
    return _encodepoint(_scalarmult(_B, a))


def ed25519_sign(msg: bytes, seed: bytes) -> bytes:
    h = hashlib.sha512(seed).digest()
    a = int.from_bytes(h[:32], 'little')
    a &= (1 << 254) - 8
    a |= 1 << 254
    prefix = h[32:]
    pub = _encodepoint(_scalarmult(_B, a))
    r = int.from_bytes(hashlib.sha512(prefix + msg).digest(), 'little') % _L
    r_pt = _encodepoint(_scalarmult(_B, r))
    k = int.from_bytes(hashlib.sha512(r_pt + pub + msg).digest(), 'little') % _L
    s = (r + k * a) % _L
    return r_pt + s.to_bytes(32, 'little')


def ed25519_verify(msg: bytes, sig: bytes, pub: bytes) -> None:
    if len(sig) != 64:
        raise ValueError('invalid signature length')
    r_bytes, s_bytes = sig[:32], sig[32:]
    s = int.from_bytes(s_bytes, 'little')
    if s >= _L:
        raise ValueError('non-canonical S')
    r_pt = _decodepoint(r_bytes)
    a_pt = _decodepoint(pub)
    k = int.from_bytes(hashlib.sha512(r_bytes + pub + msg).digest(), 'little') % _L
    lhs = _scalarmult(_B, s)
    rhs = _edwards_add(r_pt, _scalarmult(a_pt, k))
    if _encodepoint(lhs) != _encodepoint(rhs):
        raise ValueError('Ed25519 signature verification failed')


DEFAULT_SEED = hashlib.sha256(b'LogitechFlow-shuozhang7979-ed25519-feed-v1').digest()


def resolve_seed() -> bytes:
    env_hex = os.environ.get('OINK_UPDATE_SIGNING_SEED_HEX', '').strip()
    if env_hex:
        return bytes.fromhex(env_hex)
    return DEFAULT_SEED


def sign_envelope(payload_obj: dict, epoch: str, seed: bytes, pub: bytes) -> bytes:
    payload_bytes = json.dumps(payload_obj, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
    sig = ed25519_sign(payload_bytes, seed)
    ed25519_verify(payload_bytes, sig, pub)
    envelope = {
        'envelopeSchema': 1,
        'payload': base64.b64encode(payload_bytes).decode('ascii'),
        'signatures': [
            {
                'keyId': epoch,
                'ed25519': base64.b64encode(sig).decode('ascii'),
            }
        ],
    }
    return (json.dumps(envelope, indent=2) + '\n').encode('utf-8')


def package_entry(file_path: Path, url: str, epoch: str, seed: bytes, pub: bytes) -> dict:
    raw = file_path.read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    sig = ed25519_sign(raw, seed)
    ed25519_verify(raw, sig, pub)
    return {
        'url': url,
        'length': len(raw),
        'sha256': digest,
        'keyId': epoch,
        'ed25519': base64.b64encode(sig).decode('ascii'),
    }


def self_test(repo_root: Path) -> None:
    tv1_seed = bytes.fromhex('9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60')
    tv1_pub = ed25519_public_key(tv1_seed)
    assert base64.b64encode(tv1_pub).decode() == '11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo='
    tv1_sig = ed25519_sign(b'', tv1_seed)
    assert tv1_sig.hex() == (
        'e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e0652249015'
        '55fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b'
    )
    ed25519_verify(b'', tv1_sig, tv1_pub)

    trust = json.loads((repo_root / 'oink-0.3.6/packaging/update-trust.json').read_text())
    seed = resolve_seed()
    pub = ed25519_public_key(seed)
    pub_b64 = base64.b64encode(pub).decode('ascii')
    assert trust['mac']['public_key'] == pub_b64, 'mac public_key mismatch with update-trust.json'
    assert trust['windows'][0]['public_key'] == pub_b64, 'windows public_key mismatch with update-trust.json'
    assert trust['sources'][0].endswith('/releases/latest/download')
    print('generate_update_feed self-test passed!')


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--self-test', action='store_true')
    parser.add_argument('--repo', default='shuozhang7979/LogitechFlow')
    parser.add_argument('--tag')
    parser.add_argument('--mac-dmg', type=Path)
    parser.add_argument('--mac-record', type=Path)
    parser.add_argument('--win-msi', type=Path)
    parser.add_argument('--win-record', type=Path)
    parser.add_argument('--notes-file', type=Path)
    parser.add_argument('--flat-output', type=Path, help='Directory for flat GitHub Release manifest assets')
    parser.add_argument('--tree-output', type=Path, help='Directory for nested feed tree (built-installers/feed)')
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parents[1]
    if args.self_test:
        self_test(repo_root)
        return

    if not all([args.tag, args.mac_dmg, args.mac_record, args.win_msi, args.win_record, args.flat_output, args.tree_output]):
        parser.error('all release arguments are required when not running --self-test')

    trust = json.loads((repo_root / 'oink-0.3.6/packaging/update-trust.json').read_text())
    seed = resolve_seed()
    pub = ed25519_public_key(seed)
    pub_b64 = base64.b64encode(pub).decode('ascii')
    if trust['mac']['public_key'] != pub_b64 or trust['windows'][0]['public_key'] != pub_b64:
        raise RuntimeError('Signing key does not match oink-0.3.6/packaging/update-trust.json')

    notes_path = args.notes_file or (repo_root / 'oink-0.3.6/.github/update-notes.json')
    notes = json.loads(notes_path.read_text())['notes']

    mac_rec = json.loads(args.mac_record.read_text())
    win_rec = json.loads(args.win_record.read_text())

    now = datetime.now(timezone.utc).replace(microsecond=0)
    generated_at = now.strftime('%Y-%m-%dT%H:%M:%SZ')
    expires_at = (now + timedelta(days=365 * 5)).strftime('%Y-%m-%dT%H:%M:%SZ')
    sequence = int(time.time())

    base_dl = f'https://github.com/{args.repo}/releases/download/{args.tag}'
    mac_epoch = trust['mac']['epoch']
    win_epoch = trust['windows'][0]['epoch']

    mac_pkg = package_entry(args.mac_dmg, f'{base_dl}/{args.mac_dmg.name}', mac_epoch, seed, pub)
    win_pkg = package_entry(args.win_msi, f'{base_dl}/{args.win_msi.name}', win_epoch, seed, pub)

    args.flat_output.mkdir(parents=True, exist_ok=True)
    args.tree_output.mkdir(parents=True, exist_ok=True)

    def write_both(rel_path: str, content: bytes) -> None:
        nested = args.tree_output / rel_path
        nested.parent.mkdir(parents=True, exist_ok=True)
        nested.write_bytes(content)
        flat = args.flat_output / rel_path.replace('/', '-')
        flat.write_bytes(content)

    for channel in ('stable', 'beta'):
        mac_catalog = {
            'schema': 2,
            'product': 'com.hachiwii.oink',
            'platform': 'mac',
            'keyEpoch': mac_epoch,
            'architecture': 'arm64',
            'channel': channel,
            'sequence': sequence,
            'generatedAt': generated_at,
            'expiresAt': expires_at,
            'releases': [
                {
                    'build': int(mac_rec['build']),
                    'version': mac_rec['version'],
                    'tag': args.tag,
                    'channel': 'stable',
                    'sourceTreeSha256': mac_rec['source_tree_sha256'],
                    'minimumOs': str(mac_rec.get('minimum_os', '14.2')),
                    'protocolBaseline': 1,
                    'minimumUpdaterSchema': 2,
                    'notes': notes,
                    'package': mac_pkg,
                }
            ],
            'rules': [
                {'installed': [0, 2147483647], 'kind': 'normal', 'target': 'latest'},
            ],
        }
        write_both(
            f'mac/v2/{mac_epoch}/arm64/{channel}/manifest.json',
            sign_envelope(mac_catalog, mac_epoch, seed, pub),
        )

        win_min_build = int(win_rec.get('minimumWindowsBuild', 17763))
        win_catalog = {
            'schema': 2,
            'product': 'com.hachiwii.oink',
            'platform': 'windows',
            'keyEpoch': win_epoch,
            'architecture': 'x64',
            'channel': channel,
            'sequence': sequence,
            'generatedAt': generated_at,
            'expiresAt': expires_at,
            'releases': [
                {
                    'build': int(win_rec['build']),
                    'version': win_rec['version'],
                    'tag': args.tag,
                    'channel': 'stable',
                    'sourceTreeSha256': win_rec['source_tree_sha256'],
                    'minimumOs': f'10.0.{win_min_build}',
                    'protocolBaseline': 1,
                    'minimumUpdaterSchema': 2,
                    'notes': notes,
                    'package': win_pkg,
                }
            ],
            'rules': [
                {'installed': [0, 2147483647], 'kind': 'normal', 'target': 'latest'},
            ],
        }
        write_both(
            f'windows/v2/{win_epoch}/x64/{channel}/manifest.json',
            sign_envelope(win_catalog, win_epoch, seed, pub),
        )

    # Also emit Windows v1 stable manifest for completeness
    win_v1 = {
        'schema': 1,
        'product': 'com.hachiwii.oink',
        'keyEpoch': win_epoch,
        'channel': 'stable',
        'sequence': sequence,
        'generatedAt': generated_at,
        'expiresAt': expires_at,
        'releases': [
            {
                'build': int(win_rec['build']),
                'version': win_rec['version'],
                'tag': args.tag,
                'sourceTreeSha256': win_rec['source_tree_sha256'],
                'architecture': 'x64',
                'minimumWindowsBuild': int(win_rec.get('minimumWindowsBuild', 17763)),
                'protocolBaseline': 1,
                'minimumUpdaterSchema': 2,
                'channel': 'stable',
                'severity': 'normal',
                'notes': notes,
                'package': win_pkg,
            }
        ],
    }
    write_both(
        f'windows/v1/{win_epoch}/stable/manifest.json',
        sign_envelope(win_v1, win_epoch, seed, pub),
    )
    print(f'Signed v2 update catalogs generated for tag={args.tag} (mac build={mac_rec["build"]}, win build={win_rec["build"]})')


if __name__ == '__main__':
    main()
