"""Inspect the CredHist blob embedded in a master key file (no password needed)."""
from pathlib import Path

from impacket import dpapi

sid = "S-1-5-21-4093093147-2487105161-2482005045-1001"
p = Path.home() / "AppData" / "Roaming" / "Microsoft" / "Protect" / sid

for name in ("234e2725-581b-4b59-b524-e9db9f269d94", "3dbd4cf0-843a-455c-93a9-df67823b5102"):
    raw = (p / name).read_bytes()
    mkf = dpapi.MasterKeyFile(raw)
    m, b, c, d = (int(mkf[k]) for k in ("MasterKeyLen", "BackupKeyLen", "CredHistLen", "DomainKeyLen"))
    off_ch = 128 + m + b
    ch_raw = raw[off_ch:off_ch + c]
    print(f"{name[:8]} credhist off={off_ch} len={c} hex={ch_raw.hex()}")
    try:
        ch = dpapi.CredHist(ch_raw)
        print(f"   version={ch.version} entries={len(ch.credhist_entries_list)}")
        for i, e in enumerate(ch.credhist_entries_list):
            print(f"   entry#{i} sid={e.sid!r}")
    except Exception as exc:
        print(f"   parse failed: {type(exc).__name__}: {exc}")
    print()