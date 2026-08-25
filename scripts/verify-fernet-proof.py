#!/usr/bin/env python3
"""Verify a Fernet token HMAC using a key file; token is read from stdin."""
import base64, hashlib, hmac, sys
key = base64.urlsafe_b64decode(open(sys.argv[1], "rb").read().strip())
token = base64.urlsafe_b64decode(sys.stdin.buffer.read().strip())
if len(key) != 32 or len(token) < 41 or token[0] != 0x80:
    raise SystemExit("invalid Fernet proof shape")
if not hmac.compare_digest(hmac.new(key[:16], token[:-32], hashlib.sha256).digest(), token[-32:]):
    raise SystemExit("Fernet proof key mismatch")
