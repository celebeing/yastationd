#!/usr/bin/env python3
"""Compare a unit's rootfs manifest against a reference rootfs (tar or directory).

Manifest lines (made on the unit with busybox, see seed-from-unit.sh):
  F <md5>  <path>        regular file
  L <target> <path>      symlink
  D - <path>             directory
  M - <path>             mount point (/proc/mounts); nothing below one but / is compared,
                         since `find -xdev` does not descend into it on the unit
Output, one per line:
  copy <path>            file/link that is new or differs on the unit
  remove <path>          file/link in the reference that the unit no longer has
  mkdir <path>           directory only the unit has
"""
import hashlib, os, sys, tarfile

def ref_from_tar(path):
    ref = {}
    with tarfile.open(path) as t:
        for m in t:
            p = '/' + m.name.lstrip('./').rstrip('/')
            if m.isdir():
                ref[p] = ('D', '-')
            elif m.issym():
                ref[p] = ('L', m.linkname)
            elif m.isfile():
                ref[p] = ('F', hashlib.md5(t.extractfile(m).read()).hexdigest())
    return ref

def ref_from_dir(root):
    ref = {}
    for d, dirs, files in os.walk(root):
        for n in dirs + files:
            full = os.path.join(d, n)
            p = '/' + os.path.relpath(full, root)
            if os.path.islink(full):
                ref[p] = ('L', os.readlink(full))
            elif os.path.isdir(full):
                ref[p] = ('D', '-')
            elif os.path.isfile(full):
                with open(full, 'rb') as f:
                    ref[p] = ('F', hashlib.md5(f.read()).hexdigest())
    return ref

def unit_manifest(path):
    unit, mounts = {}, set()
    with open(path, errors='surrogateescape') as f:
        for line in f:
            line = line.rstrip('\n')
            kind, rest = line[:1], line[2:]
            if kind == 'F':
                h, p = rest.split('  ', 1)
                unit[p] = ('F', h)
            elif kind in ('L', 'D'):
                v, p = rest.split(' ', 1)
                unit[p] = (kind, v)
            elif kind == 'M':
                p = rest.split(' ', 1)[1]
                if p != '/':
                    mounts.add(p.rstrip('/'))
    return unit, mounts

def under_mount(p, mounts):
    return any(p.startswith(m + '/') for m in mounts)

def main():
    ref_path, man_path = sys.argv[1], sys.argv[2]
    ref = ref_from_tar(ref_path) if os.path.isfile(ref_path) else ref_from_dir(ref_path)
    unit, mounts = unit_manifest(man_path)
    out = []
    for p, (k, v) in sorted(unit.items()):
        if under_mount(p, mounts):
            continue
        r = ref.get(p)
        if k == 'D':
            if r is None:
                out.append(('mkdir', p))
        elif r != (k, v):
            out.append(('copy', p))
    for p, (k, v) in sorted(ref.items()):
        if k != 'D' and p not in unit and not under_mount(p, mounts) and p not in mounts:
            out.append(('remove', p))
    for op, p in out:
        print(op, p)

if __name__ == '__main__':
    main()
