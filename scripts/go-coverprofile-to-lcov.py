#!/usr/bin/env python3
"""Convert a merged Go coverage profile to LCOV.

Go coverage profiles describe blocks rather than lines. A line is covered when
any block spanning it has a non-zero count; taking only the last block (as some
converters do) can incorrectly mark a covered line as missed.
"""

import sys
from collections import defaultdict


def main() -> int:
    if len(sys.argv) != 3:
        print(f"usage: {sys.argv[0]} <input> <output>", file=sys.stderr)
        return 2

    lines: "defaultdict[str, dict[int, int]]" = defaultdict(dict)
    with open(sys.argv[1], encoding="utf-8") as source:
        for raw in source:
            line = raw.strip()
            if not line or line.startswith("mode:"):
                continue
            location, _, count = line.rsplit(" ", 2)
            file, span = location.split(":", 1)
            start, end = span.split(",", 1)
            start_line = int(start.split(".", 1)[0])
            end_line = int(end.split(".", 1)[0])
            covered = 1 if int(count) > 0 else 0
            for number in range(start_line, end_line + 1):
                lines[file][number] = max(lines[file].get(number, 0), covered)

    with open(sys.argv[2], "w", encoding="utf-8") as target:
        for file in sorted(lines):
            display = file.removeprefix("github.com/voidstarit/rookery/")
            file_lines = lines[file]
            target.write("TN:\n")
            target.write(f"SF:{display}\n")
            covered = 0
            for number in sorted(file_lines):
                hits = file_lines[number]
                if hits:
                    covered += 1
                target.write(f"DA:{number},{hits}\n")
            target.write(f"LF:{len(file_lines)}\n")
            target.write(f"LH:{covered}\n")
            target.write("end_of_record\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
