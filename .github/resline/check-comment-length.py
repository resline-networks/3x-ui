#!/usr/bin/env python3
"""Fail when a Go/TS comment block we touched since the upstream tag exceeds 2 lines.

CLAUDE.md: "Comments in committed Go/TS: 2 lines MAX per comment block."
Usage: .github/resline/check-comment-length.py [<base>]   (default: upstream tag of internal/config/version)
"""
import re
import subprocess
import sys

MAX_LINES = 2
EXTS = (".go", ".ts", ".tsx")


def git(*args):
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout


def default_base():
    version = open("internal/config/version").read().strip()
    return "v" + re.sub(r"-resline\.\d+$", "", version)


def added_lines(base, path):
    """Line numbers (1-based, in the working tree) that differ from base."""
    out = git("diff", "-U0", base, "--", path)
    lines = set()
    for m in re.finditer(r"^@@ -\S+ \+(\d+)(?:,(\d+))? @@", out, re.M):
        start, count = int(m.group(1)), int(m.group(2) or 1)
        lines.update(range(start, start + count))
    return lines


def comment_blocks(text):
    """Yield (first, last) 1-based line ranges of consecutive comment lines."""
    block_start = None
    in_star = False
    lines = text.splitlines()
    for i, raw in enumerate(lines, 1):
        s = raw.strip()
        is_comment = in_star or s.startswith("//") or s.startswith("/*")
        if s.startswith("/*") and "*/" not in s[2:]:
            in_star = True
        elif in_star and "*/" in s:
            in_star = False
            is_comment = True
        if is_comment and block_start is None:
            block_start = i
        if not is_comment and block_start is not None:
            yield block_start, i - 1
            block_start = None
    if block_start is not None:
        yield block_start, len(lines)


def main():
    base = sys.argv[1] if len(sys.argv) > 1 else default_base()
    changed = git("diff", "--name-only", "--diff-filter=AM", base, "--").split()
    failures = []
    for path in changed:
        if not path.endswith(EXTS) or "/node_modules/" in path or path.endswith(".pb.go"):
            continue
        touched = added_lines(base, path)
        text = open(path, encoding="utf-8").read()
        for first, last in comment_blocks(text):
            if last - first + 1 > MAX_LINES and touched & set(range(first, last + 1)):
                failures.append(f"{path}:{first}: comment block is {last - first + 1} lines (max {MAX_LINES})")
    if failures:
        print("\n".join(failures))
        print(f"\n{len(failures)} comment block(s) over {MAX_LINES} lines since {base}; see CLAUDE.md.", file=sys.stderr)
        return 1
    print(f"Comment blocks touched since {base} are within {MAX_LINES} lines.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
