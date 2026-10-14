"""End-to-end check of the rewritten vault, mimicking exactly what the plugin does:
   DPAPI(CurrentUser) -> Local State -> AES key -> auth-v2.dat -> credential JSON."""
import base64
import ctypes
import ctypes.wintypes as wintypes
import json
from pathlib import Path

VAULT = Path(r"D:\Program Files\opencode2api\bin\providers\qw2api\data\vault")


class DATA_BLOB(ctypes.Structure):
    _fields_ = [("cbData", wintypes.DWORD), ("pbData", ctypes.POINTER(ctypes.c_char))]


def crypt_unprotect(data: bytes) -> bytes:
    crypt32 = ctypes.WinDLL("crypt32.dll", use_last_error=True)
    kernel32 = ctypes.WinDLL("kernel32.dll", use_last_error=True)
    fn = crypt32.CryptUnprotectData
    fn.argtypes = [
        ctypes.POINTER(DATA_BLOB), ctypes.c_void_p, ctypes.c_void_p,
        ctypes.c_void_p, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(DATA_BLOB),
    ]
    fn.restype = wintypes.BOOL
    free = kernel32.LocalFree
    free.argtypes = [ctypes.c_void_p]
    free.restype = ctypes.c_void_p

    buf = ctypes.create_string_buffer(data, len(data))
    inb = DATA_BLOB(len(data), ctypes.cast(buf, ctypes.POINTER(ctypes.c_char)))
    outb = DATA_BLOB()
    if not fn(ctypes.byref(inb), None, None, None, None, 0, ctypes.byref(outb)):
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        return ctypes.string_at(outb.pbData, outb.cbData)
    finally:
        free(outb.pbData)


def open_credential(blob: bytes, key: bytes) -> dict:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
    if blob[:3] != b"v10":
        raise ValueError(f"magic {blob[:3]!r}")
    return json.loads(AESGCM(key).decrypt(blob[3:15], blob[15:], None).decode("utf-8"))


ok = 0
for d in sorted(p for p in VAULT.iterdir() if p.is_dir()):
    try:
        key_b64 = json.loads((d / "Local State").read_text(encoding="utf-8"))["os_crypt"]["encrypted_key"]
        raw = base64.b64decode(key_b64)
        assert raw[:5] == b"DPAPI", "not a DPAPI blob"
        aes_key = crypt_unprotect(raw[5:])
        cred = open_credential((d / "auth-v2.dat").read_bytes(), aes_key)
        email = cred.get("user", {}).get("email", "?")
        rt = "rt=yes" if cred.get("refreshToken") else "rt=NO"
        print(f"OK   {d.name[:8]}  aes={len(aes_key)}B  {email}  {rt}")
        ok += 1
    except Exception as exc:
        print(f"FAIL {d.name[:8]}  {type(exc).__name__}: {exc}")

print(f"\n{ok} / 6 vault entries are readable by the plugin's own decryption path")