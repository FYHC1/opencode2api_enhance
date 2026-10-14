"""Summarize recovered credentials without printing secrets."""
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

src = Path(sys.argv[1])
doc = json.loads(src.read_text(encoding="utf-8"))

now = datetime.now(timezone.utc)
print(f"{'email':<45} {'plan':<12} {'access exp':<26} {'refresh exp':<26} rt")
for uid, cred in doc.items():
    user = cred.get("user", {})
    email = user.get("email", "?")
    plan = user.get("planName") or user.get("tier") or "?"
    rt = "yes" if cred.get("refreshToken") else "NO"

    def parse(v):
        if not v:
            return "-"
        try:
            t = datetime.fromisoformat(v.replace("Z", "+00:00"))
            if t.tzinfo is None:
                t = t.replace(tzinfo=timezone.utc)
            return t.astimezone().strftime("%Y-%m-%d %H:%M") + ("  EXPIRED" if t < now else "")
        except Exception:
            return v[:20]

    print(f"{email:<45} {plan:<12} {parse(cred.get('expiresAt')):<26} {parse(cred.get('refreshTokenExpiresAt')):<26} {rt}")