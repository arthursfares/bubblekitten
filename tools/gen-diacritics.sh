#!/usr/bin/env bash
# Regenerates diacritics_table.go from kitty's canonical source table.
# Run from the repo root: ./tools/gen-diacritics.sh
set -euo pipefail
cd "$(dirname "$0")/.."

kitty_url="https://raw.githubusercontent.com/kovidgoyal/kitty/master/gen/rowcolumn-diacritics.txt"
codepoints_file="/tmp/kitty-diacritics-codepoints.txt"

curl -fsSL "$kitty_url" \
  | grep -v '^#' \
  | grep -v '^$' \
  | cut -d';' -f1 \
  > "$codepoints_file"

python3 - <<PYEOF
with open("$codepoints_file") as f:
    cps = [line.strip() for line in f if line.strip()]

lines = [
    "// Code generated from kitty's gen/rowcolumn-diacritics.txt (Unicode 6.0.0",
    "// combining-mark table) — DO NOT EDIT BY HAND. Regenerate via tools/gen-diacritics.sh.",
    "//",
    "// Source: https://github.com/kovidgoyal/kitty/blob/master/gen/rowcolumn-diacritics.txt",
    "package bubblekitten",
    "",
    "// diacritics maps a row or column index (0-296) to the combining Unicode",
    "// rune the kitty graphics protocol uses to encode that index on a",
    "// U+10EEEE placeholder cell. diacritics[0] is the diacritic for index 0,",
    "// diacritics[1] for index 1, and so on.",
    f"var diacritics = [{len(cps)}]rune{{",
]

per_line = 8
for i in range(0, len(cps), per_line):
    chunk = cps[i:i + per_line]
    lines.append("\t" + ", ".join(f"0x{c}" for c in chunk) + ",")
lines.append("}")
lines.append("")

with open("diacritics_table.go", "w") as f:
    f.write("\n".join(lines) + "\n")

print(f"wrote {len(cps)} entries")
PYEOF

gofmt -w diacritics_table.go
