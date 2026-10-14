"""Verify how impacket parses this machine's DPAPI master key files."""
from pathlib import Path

from impacket import dpapi

sid = "S-1-5-21-4093093147-2487105161-2482005045-1001"
p = Path.home() / "AppData" / "Roaming" / "Microsoft" / "Protect" / sid

for f in sorted(p.iterdir()):
    if f.name == "Preferred":
        continue
    raw = f.read_bytes()
    mkf = dpapi.MasterKeyFile(raw)
    m = int(mkf["MasterKeyLen"])
    b = int(mkf["BackupKeyLen"])
    c = int(mkf["CredHistLen"])
    d = int(mkf["DomainKeyLen"])
    total = 128 + m + b + c + d
    guid = mkf["Guid"].decode("utf-16le", "replace").encode("ascii", "replace").decode()
    print(f"{f.name[:8]} size={len(raw):4d} MK={m:4d} BK={b:4d} CH={c:4d} DK={d:4d} sum={total:4d} guid={guid}")