"""Duplicate/interleave S16LE mono PCM into N channels for spotter-host.

  python3 -I interleave.py N in.raw out.raw [gain_db]        # same signal in all N channels
  python3 -I interleave.py N in0.raw,in1.raw,... out.raw     # one file per channel
"""
import sys
import numpy as np

n = int(sys.argv[1])
ins = sys.argv[2].split(",")
gain = 10 ** (float(sys.argv[4]) / 20) if len(sys.argv) > 4 else 1.0
chans = [np.fromfile(p, dtype="<i2").astype(np.float32) for p in ins]
if len(chans) == 1:
    chans = chans * n
assert len(chans) == n, "need 1 or N inputs"
m = min(len(c) for c in chans)
x = np.stack([c[:m] for c in chans], axis=1) * gain
np.clip(x, -32768, 32767).astype("<i2").tofile(sys.argv[3])
