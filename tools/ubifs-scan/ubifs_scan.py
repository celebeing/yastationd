"""Index-free UBIFS recovery: scan every node, keep the newest (by sqnum) per key.

Usage: ubifs_scan.py <vol.ubifs> <outdir>
Works on a LEB-concatenated volume image (ubireader_extract_images output).
"""
import os, struct, sys, zlib
import lzallright

MAGIC = 0x06101831
img = open(sys.argv[1], 'rb').read()
out = sys.argv[2]
lzo = lzallright.LZOCompressor()

inodes = {}   # inum -> (sqnum, dict)
dents = {}    # (parent, name) -> (sqnum, inum, type)
data = {}     # (inum, block) -> (sqnum, bytes)
truns = {}    # inum -> (sqnum, new_size)

def decomp(ct, buf, size):
    if ct == 0:
        return buf
    if ct == 1:
        return lzo.decompress(buf, output_size_hint=size)
    if ct == 2:
        return zlib.decompress(buf, -15)
    raise ValueError('compr %d' % ct)

off = 0
n = 0
while off + 24 <= len(img):
    if struct.unpack_from('<I', img, off)[0] != MAGIC:
        off += 8
        continue
    crc, sqnum, ln, nt = struct.unpack_from('<IQIB', img, off + 4)
    if ln < 24 or off + ln > len(img):
        off += 8
        continue
    if (zlib.crc32(img[off + 8:off + ln]) ^ 0xFFFFFFFF) & 0xFFFFFFFF != crc:
        off += 8
        continue
    node = img[off:off + ln]
    n += 1
    if nt == 0:  # ino
        inum = struct.unpack_from('<I', node, 24)[0]
        (cs, size, at, ct, mt, an, cn, mn, nlink, uid, gid, mode, flags,
         dlen) = struct.unpack_from('<QQQQQIIIIIIIII', node, 40)
        compr = struct.unpack_from('<H', node, 136)[0]
        if inum not in inodes or inodes[inum][0] < sqnum:
            inodes[inum] = (sqnum, dict(size=size, mtime=mt, nlink=nlink, mode=mode,
                                        uid=uid, gid=gid, inline=node[160:160 + dlen]))
    elif nt == 1:  # data
        inum, k2 = struct.unpack_from('<II', node, 24)
        blk = k2 & 0x1FFFFFFF
        size, ct = struct.unpack_from('<IH', node, 40)
        try:
            buf = decomp(ct, node[48:ln], size)
        except Exception as e:
            buf = None
        key = (inum, blk)
        if buf is not None and (key not in data or data[key][0] < sqnum):
            data[key] = (sqnum, buf[:size])
    elif nt == 2:  # dent
        parent = struct.unpack_from('<I', node, 24)[0]
        inum, _, typ, nlen = struct.unpack_from('<QBBH', node, 40)
        name = node[56:56 + nlen].decode('utf-8', 'replace')
        key = (parent, name)
        if key not in dents or dents[key][0] < sqnum:
            dents[key] = (sqnum, inum, typ)
    elif nt == 4:  # trun
        inum = struct.unpack_from('<I', node, 24)[0]
        new_size = struct.unpack_from('<Q', node, 48)[0]
        if inum not in truns or truns[inum][0] < sqnum:
            truns[inum] = (sqnum, new_size)
    off += (ln + 7) & ~7

print('nodes', n, 'inodes', len(inodes), 'dents', len(dents), 'data', len(data))

children = {}
for (parent, name), (sq, inum, typ) in dents.items():
    if inum == 0:
        continue  # deletion entry
    children.setdefault(parent, []).append((name, inum, typ))

def walk(inum, path, depth=0):
    if depth > 30:
        return
    for name, ci, typ in sorted(children.get(inum, [])):
        p = os.path.join(path, name)
        ino = inodes.get(ci, (0, None))[1]
        mode = ino['mode'] if ino else 0
        fmt = mode & 0o170000
        if fmt == 0o040000 or (not ino and typ == 1):
            os.makedirs(p, exist_ok=True)
            walk(ci, p, depth + 1)
        elif fmt == 0o120000:
            tgt = ino['inline'].decode('utf-8', 'replace')
            print('LINK', os.path.relpath(p, out), '->', tgt)
        elif fmt == 0o100000 or typ == 0:
            size = ino['size'] if ino else 0
            buf = bytearray(size)
            missing = 0
            for b in range((size + 4095) // 4096):
                d = data.get((ci, b))
                if d is None:
                    missing += 1
                    continue
                chunk = d[1][:max(0, size - b * 4096)]
                buf[b * 4096:b * 4096 + len(chunk)] = chunk
            with open(p, 'wb') as f:
                f.write(buf)
            print('FILE %8d %s%s' % (size, os.path.relpath(p, out),
                                     ' [missing %d blk]' % missing if missing else ''))
        else:
            print('SPECIAL %o %s' % (mode, os.path.relpath(p, out)))

os.makedirs(out, exist_ok=True)
walk(1, out)
