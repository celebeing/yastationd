#!/usr/bin/env python3
"""mkgolden.py <work-dir> <platform>: build a golden restore package, run by tools/golden.sh in the
golden image, under fakeroot (docs/guides/golden-image.md).

In <work-dir>: rootfs.tar (the donor's rootfs), cfw.tar (its <cfw>, the allowed part), stock.img
(the donor's restore package). Out: golden.img, golden.txt.

The package's items other than `system` stay byte for byte. `system` is rebuilt with the geometry of
the one it replaces, read from that item, so nothing about a board is written down here.
"""
import hashlib
import os
import pathlib
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import zlib

MAGIC = 0x27B51956
ITEM, TABLE = 576, 64
SPARSE = 0xED26FF3A
HERE = pathlib.Path(__file__).resolve().parent
INITD = HERE.parent.parent / "yastationd" / "init.d"


def die(msg):
    sys.exit("mkgolden: " + msg)


def run(*cmd, **kw):
    print("  $", " ".join(str(c) for c in cmd), flush=True)
    return subprocess.run([str(c) for c in cmd], check=True, **kw)


# ---- the package ---------------------------------------------------------------------------

def parse(img):
    _, ver, magic, size, _, count = struct.unpack_from("<IIIQII", img, 0)
    if magic != MAGIC or ver != 2:
        die("not an Amlogic v2 package")
    items = []
    for i in range(count):
        o = TABLE + i * ITEM
        _, _, _, off, sz = struct.unpack_from("<IIQQQ", img, o)
        main = img[o + 32 : o + 288].split(b"\0")[0].decode()
        sub = img[o + 288 : o + 544].split(b"\0")[0].decode()
        items.append({"o": o, "off": off, "sz": sz, "main": main, "sub": sub})
    return size, items


def find(items, main, sub):
    return next(x for x in items if x["main"] == main and x["sub"] == sub)


def recrc(out):
    struct.pack_into("<I", out, 0, ~zlib.crc32(bytes(out[4:])) & 0xFFFFFFFF)


def repack(stock, new):
    """The package with `system` replaced: its VERIFY, the sizes and the CRC follow."""
    size, items = parse(stock)
    img = stock[:size]
    sysit, ver = find(items, "PARTITION", "system"), find(items, "VERIFY", "system")
    tail = sysit["off"] + sysit["sz"]
    if ver["off"] != tail:
        die("no VERIFY right after `system`: an unknown package layout")
    delta = len(new) - sysit["sz"]
    if delta % 16:
        die("the new `system` breaks the items' alignment")
    verify = b"sha1sum " + hashlib.sha1(new).hexdigest().encode()
    if len(verify) != ver["sz"]:
        die("unexpected VERIFY size")
    rest = bytearray(img[tail:])
    rest[: ver["sz"]] = verify
    out = bytearray(img[: sysit["off"]]) + new + rest
    for x in items:
        if x["off"] >= tail:
            struct.pack_into("<Q", out, x["o"] + 16, x["off"] + delta)
    struct.pack_into("<Q", out, sysit["o"] + 24, len(new))
    # The item naming the package itself (offset 0, the whole size), and the header.
    for x in items:
        if x["sz"] == size and x["off"] == 0:
            struct.pack_into("<Q", out, x["o"] + 24, len(out))
    struct.pack_into("<Q", out, 12, len(out))
    recrc(out)
    return bytes(out)


def check_stock(stock):
    """The header CRC and every VERIFY must come out as the donor's have them: else we don't
    understand this package and must not write one like it."""
    size, items = parse(stock)
    img = bytearray(stock[:size])
    crc = bytes(img[:4])
    recrc(img)
    if bytes(img[:4]) != crc:
        die("the stock package's CRC does not recompute")
    for v in (x for x in items if x["main"] == "VERIFY"):
        p = find(items, "PARTITION", v["sub"])
        want = b"sha1sum " + hashlib.sha1(stock[p["off"] : p["off"] + p["sz"]]).hexdigest().encode()
        if stock[v["off"] : v["off"] + v["sz"]] != want:
            die("VERIFY of " + v["sub"] + " does not recompute")
    if repack(stock, stock[find(items, "PARTITION", "system")["off"]:][: find(items, "PARTITION", "system")["sz"]]) != stock[:size]:
        die("repacking the stock package does not reproduce it")


# ---- the rootfs tree ---------------------------------------------------------------------------

REQUIRED = {  # what must be on the donor: the CFW's own rootfs edits
    "etc/init.d/S42zdropbear": b"ssh.sh",
    "etc/init.d/S52zyastationd": b"yastationd",
    "etc/init.d/S99zzegress": b"egress.sh",
    "system/vendor/quasar/run.sh": b"maind_off",
}
FORBIDDEN = re.compile(r"(host_key|web\.secret|yastationd\.(conf|state)|wpa_supplicant\.conf|sendspin/|voices/|"
                       r"trusted_user_ca|principals|sshd_config\.local|\.prev$|\.bak|\.bad$)")


PRIVATE = re.compile(r"\b(192\.168|10|172\.(1[6-9]|2\d|3[01]))\.\d+\.\d+")


def tree(work, platform):
    t = work / "tree"
    shutil.rmtree(t, ignore_errors=True)
    t.mkdir()
    run("tar", "xpf", work / "rootfs.tar", "--numeric-owner", "-C", t)
    for f, needle in REQUIRED.items():
        if needle not in (t / f).read_bytes():
            die(f"the donor has no CFW edit in /{f}: install the CFW on it first")
    if b"network=" in (t / "etc/wpa_supplicant.conf").read_bytes():
        die("/etc/wpa_supplicant.conf of the donor has a network: not for a shared image")
    ota = t / "system/vendor/quasar/device_ota_update.sh"
    if ota.exists() and b"yandex-apply-ota" in ota.read_bytes():
        die("the donor's update stub is not in place")

    # What one owner's unit left behind. chrony.conf: the install points it at the home router.
    chrony = t / "etc/chrony.conf"
    if chrony.exists():
        chrony.write_text("".join(l for l in chrony.read_text().splitlines(True)
                                  if not (l.startswith("server ") and PRIVATE.search(l))))
    for rel in ("usr/share/cfw-seed", "etc/cfw-image-marker", "etc/cfw-image"):
        p = t / rel
        shutil.rmtree(p) if p.is_dir() else p.unlink(missing_ok=True)
    ssh = t / "root/.ssh"
    shutil.rmtree(ssh, ignore_errors=True)
    ssh.mkdir(mode=0o700)

    cfwroot = "/bkupgrade/cfw" if os.path.lexists(t / "bkupgrade") else "/data/cfw"
    (ssh / "authorized_keys").symlink_to(cfwroot + "/authorized_keys")

    seed = t / "usr/share/cfw-seed"
    seed.mkdir()
    run("tar", "xpf", work / "cfw.tar", "--numeric-owner", "--no-same-owner", "-C", seed)
    for p in seed.rglob("*"):
        rel = p.relative_to(seed).as_posix()
        if FORBIDDEN.search(rel) and not rel.endswith(".orig"):
            die("an owner's file in the seed: " + rel)
    for need in ("bin/yastationd", "bin/hostapd", "bin/dropbearmulti", "init.d/ssh.sh", "init.d/supervise.sh",
                 "openssh/sshd", "egress.sh"):
        if not (seed / need).exists():
            die("the seed lacks " + need + ": deploy the CFW to the donor first")
    (seed / "maind_off").touch()
    (seed / "ssh_server").write_text("openssh\n")
    (seed / "yastationd.conf").write_text(
        f"# Written by the golden image's first boot (docs/guides/golden-image.md). Set the rest on the\n"
        f"# setup page, or put a backup back with tools/restore-unit.sh.\n"
        f"device_id   = {platform}-@ID@\n"
        f"device_name = Yandex Station {platform}\n")
    shutil.copy(INITD / "S00zcfwseed", t / "etc/init.d/S00zcfwseed")
    os.chmod(t / "etc/init.d/S00zcfwseed", 0o755)
    ver = (work / "version").read_text().strip()
    for p in (t / "etc").rglob("*"):  # to look at: LAN addresses left in the donor's /etc
        if p.is_file() and not p.is_symlink() and p.stat().st_size < 65536 and PRIVATE.search(p.read_bytes().decode("latin1")):
            print("  note: a private address in /" + p.relative_to(t).as_posix(), flush=True)
    (t / "etc/cfw-image").write_text(f"golden {platform} {ver} seq={SEQ}\n")
    return t


# ---- system images -----------------------------------------------------------------------------

SEQ = int.from_bytes(os.urandom(4), "big") & 0x7FFFFFFF


def ubi_geometry(item):
    if item[:4] != b"UBI#":
        die("not a UBI image")
    vid, data = struct.unpack(">II", item[16:24])
    peb = next(p for p in range(0x1000, 0x400000, 0x1000) if item[p:p + 4] == b"UBI#")
    v = item[vid : vid + 64]
    if v[:4] != b"UBI!":
        die("no VID header")
    leb0 = next(i for i in range(len(item) // peb)
                if struct.unpack(">II", item[i * peb + vid + 8 : i * peb + vid + 16]) == (0, 0))
    s = item[leb0 * peb + data :]
    u = lambda o: struct.unpack_from("<I", s, o)[0]
    if u(0) != 0x06101831 or s[20] != 6:
        die("no UBIFS superblock in the stock item")
    g = dict(peb=peb, vid=vid, min_io=u(32), leb=u(36), max_leb=u(44), log=u(56), fanout=u(72),
             fmt=u(80), compr=struct.unpack_from("<H", s, 84)[0], flags=u(28))
    if g["flags"] != 0 or g["fmt"] != 4:
        die(f"unexpected UBIFS superblock: {g}")
    if g["compr"] != 2:
        die("the stock image is not zlib: extend mkgolden")
    # The volume's flags and name, from the volume table.
    vt = next(i for i in range(len(item) // peb)
              if struct.unpack(">I", item[i * peb + vid + 8 : i * peb + vid + 12])[0] == 0x7FFFEFFF)
    r = item[vt * peb + data : vt * peb + data + 172]
    g["name"] = r[16:144].split(b"\0")[0].decode()
    g["autoresize"] = bool(r[144] & 1)
    if g["min_io"] != g["vid"]:
        die("VID offset != min I/O: extend mkgolden")
    return g


def build_ubi(work, t, item):
    g = ubi_geometry(item)
    print("  stock UBI:", g, flush=True)
    fs, img = work / "rootfs.ubifs", work / "system.new"
    run("mkfs.ubifs", "-r", t, "-m", g["min_io"], "-e", g["leb"], "-c", g["max_leb"], "-x", "zlib",
        "-f", g["fanout"], "-l", g["log"], "-o", fs)
    cfg = work / "ubinize.cfg"
    cfg.write_text(f"[{g['name']}]\nmode=ubi\nvol_id=0\nvol_type=dynamic\nvol_name={g['name']}\n"
                   f"vol_alignment=1\n{'vol_flags=autoresize' if g['autoresize'] else ''}\nimage={fs}\n")
    run("ubinize", "-o", img, "-p", g["peb"], "-m", g["min_io"], "-s", g["min_io"], "-O", g["vid"],
        "-Q", SEQ, cfg)
    return img.read_bytes()


def build_ext4(work, t, item):
    raw_stock, raw = work / "stock.ext4", work / "system.ext4"
    (work / "stock.sparse").write_bytes(item)
    run("simg2img", work / "stock.sparse", raw_stock)
    info = subprocess.run(["dumpe2fs", "-h", str(raw_stock)], capture_output=True, text=True, check=True).stdout
    get = lambda k: re.search(rf"^{k}:\s*(.*)$", info, re.M).group(1).strip()
    feats = [f for f in get("Filesystem features").split() if f not in ("needs_recovery",)]
    blocks, bs = int(get("Block count")), int(get("Block size"))
    uuid, label, isize = get("Filesystem UUID"), get("Filesystem volume name"), get("Inode size")
    args = ["mke2fs", "-q", "-t", "ext4", "-F", "-b", bs, "-I", isize, "-U", uuid, "-O", "none," + ",".join(feats),
            "-E", "lazy_itable_init=0,lazy_journal_init=0", "-d", t]
    if label and label != "<none>":
        args += ["-L", label]
    run(*args, raw, blocks)
    run("e2fsck", "-fn", raw)
    run("img2simg", raw, work / "system.new")
    return (work / "system.new").read_bytes()


def verify_ubi(work, t, new):
    """Unpack the new image with another tool: the same entries, owners, modes and contents."""
    (work / "system.new").write_bytes(new)
    out = work / "check"
    shutil.rmtree(out, ignore_errors=True)
    run("ubireader_extract_files", "-k", "-o", out, work / "system.new", stdout=subprocess.DEVNULL)
    got = next(out.glob("*/*"))

    def listing(root):
        r = {}
        for p in [root, *root.rglob("*")]:
            st = p.lstat()
            kind = "l" + os.readlink(p) if p.is_symlink() else "d" if p.is_dir() else hashlib.sha256(p.read_bytes()).hexdigest()
            r[p.relative_to(root).as_posix()] = (st.st_mode & 0o7777 if not p.is_symlink() else 0, st.st_uid, st.st_gid, kind)
        return r

    a, b = listing(t), listing(got)
    if a != b:
        bad = sorted(k for k in a.keys() | b.keys() if a.get(k) != b.get(k))
        die(f"the unpacked image differs from the tree in {len(bad)} entries: {bad[:8]}")
    print(f"  unpacked image matches the tree: {len(a)} entries", flush=True)


def main():
    work, platform = pathlib.Path(sys.argv[1]).resolve(), sys.argv[2]
    stock = (work / "stock.img").read_bytes()
    print("== stock package", flush=True)
    check_stock(stock)
    size, items = parse(stock)
    s = find(items, "PARTITION", "system")
    item = stock[s["off"] : s["off"] + s["sz"]]
    print("  CRC, VERIFY and repacking reproduce it,", len(items), "items,", size, "bytes", flush=True)
    print("== rootfs", flush=True)
    t = tree(work, platform)
    print("== system", flush=True)
    sparse = struct.unpack_from("<I", item, 0)[0] == SPARSE
    new = build_ext4(work, t, item) if sparse else build_ubi(work, t, item)
    if not sparse:
        verify_ubi(work, t, new)
    print("== package", flush=True)
    out = repack(stock, new)
    _, oi = parse(out)
    for x in items:
        if x["sub"] in ("system",) or x["off"] == 0:
            continue
        y = find(oi, x["main"], x["sub"])
        if out[y["off"] : y["off"] + y["sz"]] != stock[x["off"] : x["off"] + x["sz"]]:
            die("item changed: " + x["sub"])
    (work / "golden.img").write_bytes(out)
    sha = hashlib.sha256(out).hexdigest()
    (work / "golden.txt").write_text(
        f"platform {platform}\nversion {(work / 'version').read_text().strip()}\nsize {len(out)}\n"
        f"sha256 {sha}\nimage_seq {SEQ}\nsystem {len(new)} (stock {len(item)})\n")
    print((work / "golden.txt").read_text())


main()
