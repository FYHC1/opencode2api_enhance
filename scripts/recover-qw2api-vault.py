#!/usr/bin/env python3
"""Recover qw2api vault credentials after a Windows DPAPI master-key rotation.

Chain (matches qw2api-public/src/internal/auth):
  1. DPAPI master key file  -> decrypted by impacket (needs the OLD Windows password)
  2. vault/<uid>/Local State-> DPAPI blob -> AES-256 master key
  3. vault/<uid>/auth-v2.dat-> v10 + nonce(12) + AES-256-GCM(cipher||tag) -> Credential JSON

Steps 2 and 3 are implemented here and are safe to trust (format taken from
credential.go). Step 1 needs impacket:  pip install impacket

ASCII-only output on purpose: the console here is GBK and would mangle CJK.

Usage:
    python recover-qw2api-vault.py --out recovered.json
    python recover-qw2api-vault.py --out recovered.json --write-back
    python recover-qw2api-vault.py --self-test
"""
import argparse
import base64
import ctypes
import ctypes.wintypes as wintypes
import getpass
import json
import os
import shutil
import struct
import sys
import time
import uuid
from pathlib import Path

DEFAULT_VAULT = Path(r"D:\Program Files\opencode2api\bin\providers\qw2api\data\vault")
KEY_DATA_LEN = 76


# --------------------------------------------------------------------------- #
# DPAPI blob helpers (Chromium "Local State" os_crypt.encrypted_key)
# --------------------------------------------------------------------------- #
def split_dpapi_blob(encrypted_key_b64: str):
    raw = base64.b64decode(encrypted_key_b64)
    if len(raw) < 5 or raw[:5] != b"DPAPI":
        raise ValueError("encrypted_key is not a DPAPI blob")
    return raw[5:]


def master_key_guid(blob: bytes) -> str:
    """GUID lives at offset 24 of a CryptProtectData blob."""
    d1, d2, d3 = struct.unpack("<IHH", blob[24:32])
    tail = blob[32:40]
    tail_hex = "".join(f"{b:02x}" for b in tail)
    return f"{d1:08x}-{d2:04x}-{d3:04x}-{tail_hex[:4]}-{tail_hex[4:]}"


def crypt_protect(data: bytes) -> bytes:
    """CryptProtectData, CurrentUser scope, no entropy (same call the plugin makes)."""

    class DATA_BLOB(ctypes.Structure):
        _fields_ = [("cbData", wintypes.DWORD), ("pbData", ctypes.POINTER(ctypes.c_char))]

    crypt32 = ctypes.WinDLL("crypt32.dll", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32.dll", use_last_error=True)
    CryptProtectData = crypt32.CryptProtectData
    CryptProtectData.argtypes = [
        ctypes.POINTER(DATA_BLOB), wintypes.LPCWSTR, ctypes.POINTER(DATA_BLOB),
        ctypes.c_void_p, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(DATA_BLOB),
    ]
    CryptProtectData.restype = wintypes.BOOL
    LocalFree = kernel32.LocalFree
    LocalFree.argtypes = [ctypes.c_void_p]
    LocalFree.restype = ctypes.c_void_p

    in_buf = ctypes.create_string_buffer(data, len(data))
    in_blob = DATA_BLOB(len(data), ctypes.cast(in_buf, ctypes.POINTER(ctypes.c_char)))
    out_blob = DATA_BLOB()
    if not CryptProtectData(ctypes.byref(in_blob), None, None, None, None, 0, ctypes.byref(out_blob)):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        return ctypes.string_at(out_blob.pbData, out_blob.cbData)
    finally:
        LocalFree(out_blob.pbData)


# --------------------------------------------------------------------------- #
# qw2api credential format (credential.go)
# --------------------------------------------------------------------------- #
def seal_credential(cred: dict, aes_key: bytes) -> bytes:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    gcm = AESGCM(aes_key)
    nonce = os.urandom(12)
    sealed = gcm.encrypt(nonce, json.dumps(cred).encode("utf-8"), None)
    return b"v10" + nonce + sealed  # sealed already carries the 16-byte tag


def open_credential(blob: bytes, aes_key: bytes) -> dict:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    if blob[:3] != b"v10":
        raise ValueError(f"unexpected auth-v2.dat magic {blob[:3]!r}")
    nonce, sealed = blob[3:15], blob[15:]
    plain = AESGCM(aes_key).decrypt(nonce, sealed, None)
    return json.loads(plain.decode("utf-8"))


def write_local_state(directory: Path, aes_key: bytes) -> None:
    blob = crypt_protect(aes_key)
    encoded = base64.b64encode(b"DPAPI" + blob).decode("ascii")
    doc = {"os_crypt": {"encrypted_key": encoded}}
    (directory / "Local State").write_text(json.dumps(doc), encoding="utf-8")


# --------------------------------------------------------------------------- #
# step 1: DPAPI master key (impacket)
# --------------------------------------------------------------------------- #
def recover_master_key(mk_path: Path, sid: str, password: str, credhist: bytes):
    """Step 1: decrypt a DPAPI master key file with the OLD Windows password.

    impacket API (verified against the installed package):
      deriveKeysFromUser(sid, password) -> [sha1_key, md4_key, protected_user_key]
      MasterKeyFile  -> header with the per-section lengths
      MasterKey(blob) -> .decrypt(derivedKey) -> 64-byte key, or None on HMAC failure
    """
    try:
        from impacket import dpapi
    except ImportError:
        sys.exit("impacket is required for step 1:  pip install impacket")

    derived_keys = list(dpapi.deriveKeysFromUser(sid, password))

    if credhist:
        # CredHist holds password rotations; try every entry the file offers.
        try:
            ch = dpapi.CredHist(credhist)
            for entry in getattr(ch, "entries", []) or []:
                pwdhash = getattr(entry, "pwdhash", None)
                if pwdhash:
                    derived_keys += list(dpapi.deriveKeysFromUserkey(sid, pwdhash))
        except Exception as exc:  # best effort, never fatal
            print(f"  (CredHist present but unusable: {exc})")

    raw = mk_path.read_bytes()
    mkf = dpapi.MasterKeyFile(raw)

    # Section order inside the file is not encoded in the structure, so try the
    # plausible placements of the MasterKey section until one decrypts cleanly.
    header_len = 128
    backup = int(mkf["BackupKeyLen"])
    cred = int(mkf["CredHistLen"])
    domain_key = int(mkf["DomainKeyLen"])
    master_len = int(mkf["MasterKeyLen"])
    candidates = []
    for offset in (header_len, header_len + backup, header_len + cred,
                   header_len + domain_key, header_len + backup + cred,
                   header_len + backup + cred + domain_key):
        if 0 < offset and offset + master_len <= len(raw):
            candidates.append(offset)

    for derived in derived_keys:
        for offset in candidates:
            try:
                mk = dpapi.MasterKey(raw[offset:offset + master_len])
                out = mk.decrypt(derived)
            except Exception:
                continue
            if out:
                return out
    return None


def decrypt_local_state(master_key: bytes, encrypted_key_b64: str) -> bytes:
    """Step 2: DPAPI master key -> 32-byte AES key for auth-v2.dat."""
    from impacket import dpapi

    blob = split_dpapi_blob(encrypted_key_b64)
    key = dpapi.DPAPI_BLOB(blob).decrypt(master_key)
    if key is None or len(key) != 32:
        raise ValueError(f"master key length {0 if key is None else len(key)}, expected 32")
    return key


# --------------------------------------------------------------------------- #
# main flow
# --------------------------------------------------------------------------- #
def read_json_field(path: Path, field: str) -> str:
    doc = json.loads(path.read_text(encoding="utf-8"))
    return doc["os_crypt"]["encrypted_key"]


def protect_dir(sid: str | None = None) -> Path:
    """Locate %APPDATA%\\Microsoft\\Protect\\<SID>.

    Avoids shelling out to whoami: spawning helper processes is not always
    available, and that directory is the source of truth anyway.
    """
    root = Path(os.environ["APPDATA"]) / "Microsoft" / "Protect"
    if sid:
        return root / sid
    candidates = [p for p in root.iterdir() if p.is_dir()]
    if len(candidates) == 1:
        return candidates[0]
    if not candidates:
        raise SystemExit(f"no SID directory under {root}")
    names = ", ".join(p.name for p in candidates)
    raise SystemExit(f"several SID directories found ({names}); pass --sid to pick one")


def self_test() -> None:
    """Round-trip credential sealing/opening with a random key."""
    key = os.urandom(32)
    cred = {"schemaVersion": 1, "token": "t-123", "refreshToken": "ory_rt_abc", "user": {"email": "a@b.c"}}
    blob = seal_credential(cred, key)
    assert blob[:3] == b"v10" and len(blob) == 3 + 12 + len(json.dumps(cred)) + 16
    assert open_credential(blob, key) == cred
    try:
        open_credential(blob, os.urandom(32))
        raise AssertionError("wrong key should not decrypt")
    except Exception:
        pass
    # master key GUID extraction on a synthetic blob
    synth = b"\x01\x00\x00\x00" + b"\x00" * 20 + b"\x11" * 16 + b"\x00" * 8
    assert master_key_guid(synth).startswith("11111111-")
    print("self-test OK")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--vault", default=str(DEFAULT_VAULT))
    ap.add_argument("--password", default=None, help="omit to be prompted")
    ap.add_argument("--domain", default=os.environ.get("COMPUTERNAME", ""))
    ap.add_argument("--sid", help="user SID directory name under Microsoft\\Protect")
    ap.add_argument("--out", default="recovered-credentials.json")
    ap.add_argument("--write-back", action="store_true",
                    help="re-encrypt recovered creds back into the vault (backs up first)")
    ap.add_argument("--self-test", action="store_true")
    args = ap.parse_args()

    if args.self_test:
        self_test()
        return 0

    vault = Path(args.vault)
    if not vault.is_dir():
        sys.exit(f"vault dir not found: {vault}")

    password = args.password or getpass.getpass("OLD Windows password: ")

    pdir = protect_dir(args.sid)
    print(f"protect dir : {pdir}")
    print(f"credhist    : {'present' if (pdir / 'CredHist').exists() else 'MISSING (falls back to current-password derivation)'}")

    credhist = (pdir / "CredHist").read_bytes() if (pdir / "CredHist").exists() else None
    recovered, failed = {}, []
    if credhist is None:
        print("         : no CredHist file -> only the current-password derivation is tried")
    master_keys = {}  # guid -> decrypted master key, reused across accounts

    for uid_dir in sorted(p for p in vault.iterdir() if p.is_dir()):
        ls, dat = uid_dir / "Local State", uid_dir / "auth-v2.dat"
        if not (ls.exists() and dat.exists()):
            failed.append((uid_dir.name, "missing Local State or auth-v2.dat"))
            continue
        try:
            blob = split_dpapi_blob(read_json_field(ls, "encrypted_key"))
            guid = master_key_guid(blob)
            mk_path = pdir / guid
            if not mk_path.exists():
                failed.append((uid_dir.name, f"master key {guid} not on disk"))
                continue
            if guid not in master_keys:
                master_keys[guid] = recover_master_key(mk_path, pdir.name, password, credhist)
            mk = master_keys[guid]
            if not mk:
                failed.append((uid_dir.name, "old password does not unlock this master key"))
                continue
            mk = master_keys[guid]
            if not mk:
                failed.append((uid_dir.name, "old password does not unlock this master key"))
                continue
            aes_key = decrypt_local_state(mk, read_json_field(ls, "encrypted_key"))
            cred = open_credential(dat.read_bytes(), aes_key)
            recovered[uid_dir.name] = {"credential": cred, "aes_key": aes_key.hex()}
            email = cred.get("user", {}).get("email", "?")
            print(f"OK   {uid_dir.name}  {email}  token={cred.get('token','')[:12]}...")
        except Exception as exc:  # noqa: BLE001 - report and continue
            failed.append((uid_dir.name, f"{type(exc).__name__}: {exc}"))

    print()
    print(f"recovered {len(recovered)} / {len(recovered) + len(failed)}")
    for uid, why in failed:
        print(f"FAIL {uid}: {why}")

    if not recovered:
        print("\nNothing recovered. Most likely cause: the password is not the one that")
        print("protected the master key at the time (master key rotation wiped history).")
        return 2

    out = Path(args.out)
    out.write_text(json.dumps(
        {uid: v["credential"] for uid, v in recovered.items()},
        indent=2, ensure_ascii=False,
    ), encoding="utf-8")
    print(f"\nplaintext credentials written to: {out.resolve()}  (KEEP IT SECRET)")
    for uid, v in recovered.items():
        print(f"  {uid}: {v['credential'].get('user', {}).get('email', '?')}")

    if args.write_back:
        stamp = time.strftime("%Y%m%d-%H%M%S")
        for uid, v in recovered.items():
            d = vault / uid
            for name in ("Local State", "auth-v2.dat"):
                src = d / name
                if src.exists():
                    shutil.copy2(src, d / f"{name}.{stamp}.bak")
            key = bytes.fromhex(v["aes_key"])
            write_local_state(d, key)
            (d / "auth-v2.dat").write_bytes(seal_credential(v["credential"], key))
            print(f"rewrote {uid} (new master key, old files kept as .{stamp}.bak)")
        print("\nRestart the plugin; the accounts should load again.")

    return 0


if __name__ == "__main__":
    sys.exit(main())