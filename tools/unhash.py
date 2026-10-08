"""Translate a hashed .qmd (QMLDiff) back to readable names using a device hashtab.

    python unhash.py <hashtab> <file.qmd>
Hashtab entries: u64 hash (big-endian), u32 length, UTF-8 string. The output contains names
from reMarkable's own QML: for local reference only, don't publish it.
"""
import re, struct, sys
def load(path):
    d = open(path, "rb").read(); i = 0; tab = {}
    while i + 12 <= len(d):
        h, n = struct.unpack_from(">QI", d, i); i += 12
        tab[h] = d[i:i + n].decode("utf-8", "replace"); i += n
    return tab
tab = load(sys.argv[1]); src = open(sys.argv[2]).read()
name = lambda m: tab.get(int(m.group(2)), m.group(0))
src = re.sub(r'\[\[(")?(\d+)\]\]', lambda m: ('"%s"' if m.group(1) else '%s') % name(m), src)
src = re.sub(r'~&(")?(\d+)&~', lambda m: ('"%s"' if m.group(1) else '%s') % name(m), src)
print(src)
