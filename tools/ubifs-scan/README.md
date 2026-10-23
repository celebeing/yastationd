# ubifs-scan

Recovers files from a UBIFS volume that was dumped **while mounted**. In that case the on-flash index
is stale and `ubireader_extract_files` reports "No inodes found". The script ignores the index: it
walks every node, checks its CRC, keeps the newest version (by `sqnum`) of each inode, data block and
directory entry, and rebuilds the tree from the directory entries. Compression: none, LZO, zlib (zstd is
not handled).

```sh
python3 -m venv venv && venv/bin/pip install ubi_reader==0.8.10   # pulls lzallright
venv/bin/ubireader_extract_images -o img mtd10_data.img            # UBI -> LEB-concatenated volume
venv/bin/python -I ubifs_scan.py img/mtd10_data.img/img-*_vol-data.ubifs out/ > out.list
```

`out.list` lists every file it rebuilt and flags any data blocks it could not find. Deleted files can
come back if their directory entry is still the newest node for that name. Symlinks are listed, not
created. Used on the pickle `/data` dump ([`docs/boards/pickle.md`](../../docs/boards/pickle.md)).
