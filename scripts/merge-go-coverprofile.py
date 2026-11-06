#!/usr/bin/env python3
"""Merge duplicate blocks in a Go coverage profile.

`go test -coverpkg=./...` can emit the same coverage block from several test
binaries. Summing those entries produces a profile that the LCOV converter and
diff-cover interpret correctly.
"""

import sys
from collections import OrderedDict


def main() -> int:
    if len(sys.argv) != 3:
        print(f"usage: {sys.argv[0]} <input> <output>", file=sys.stderr)
        return 2

    mode = "mode: set"
    blocks: "OrderedDict[str, tuple[int, int]]" = OrderedDict()
    with open(sys.argv[1], encoding="utf-8") as source:
        for raw in source:
            line = raw.strip()
            if not line:
                continue
            if line.startswith("mode:"):
                mode = line
                continue
            location, statements, count = line.rsplit(" ", 2)
            statements = int(statements)
            count = int(count)
            previous = blocks.get(location)
            if previous is None:
                blocks[location] = (statements, count)
            else:
                blocks[location] = (previous[0], previous[1] + count)

    with open(sys.argv[2], "w", encoding="utf-8") as target:
        target.write(mode + "\n")
        for location in sorted(blocks, key=lambda value: (value.split(":", 1)[0], value)):
            statements, count = blocks[location]
            target.write(f"{location} {statements} {count}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
