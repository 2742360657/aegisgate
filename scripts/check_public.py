#!/usr/bin/env python3
"""Check indexed files or reachable commits for common publication-boundary mistakes.

This is a small project guard, not a comprehensive secret scanner. It never
prints matched content, author addresses, or credentials.
"""
import argparse
from pathlib import PurePosixPath
import re
import subprocess
import sys

PRIVATE_PARTS = {".private", ".local", "private", "local-context", "missions", "runs", ".ssh"}
PRIVATE_NAMES = {"mission.json", "task.json", "agent-context.md", "collaboration.md"}
PATTERNS = [
    ("personal home path", re.compile(r"/(?:home|Users)/[^\s/]+/")),
    ("Windows absolute path", re.compile(r"(?<![A-Za-z0-9])[A-Za-z]:[\\/]")),
    ("WSL machine path", re.compile(r"\\\\wsl(?:\$|\.localhost)\\", re.I)),
    ("internal project identifier", re.compile(r"\b(?:personal|local)--[A-Za-z0-9]")),
    ("private coordination command", re.compile(r"workspace[ ]mission[ ](?:show|apply)|WORKSPACE[_]SESSION[=]")),
    ("personal study schedule", re.compile(r"每周\s*\d+\s*[-–—]\s*\d+\s*小时")),
    ("personal profile", re.compile(r"网安(?:的)?大三")),
    ("private key", re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----")),
    ("GitHub token", re.compile(r"gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,}")),
]
BAD_PLACEHOLDER = re.compile(r"@localhost$|\.invalid$|\(none\)$", re.I)


def git(*arguments):
    return subprocess.check_output(["git", *arguments], stderr=subprocess.PIPE)


def private_path(path):
    parts = {part.lower() for part in PurePosixPath(path).parts}
    name = PurePosixPath(path).name.lower()
    return (
        bool(parts & PRIVATE_PARTS)
        or name in PRIVATE_NAMES
        or path.lower() == "docs/learning.md"
        or name.endswith((".bundle", ".log"))
        or (name.startswith(".env") and not name.endswith((".example", ".sample", ".template")))
    )


def content_findings(text):
    return [(text.count("\n", 0, match.start()) + 1, label)
            for label, pattern in PATTERNS for match in pattern.finditer(text)]


def check_tree(tree, label, cache):
    errors = []
    for entry in git("ls-tree", "-rz", "--full-tree", tree).split(b"\0"):
        if not entry:
            continue
        metadata, encoded_path = entry.split(b"\t", 1)
        _, kind, oid = metadata.decode().split()
        path = encoded_path.decode("utf-8", errors="replace")
        if private_path(path):
            errors.append(f"{label}:{path}: local-only file")
        if kind != "blob":
            errors.append(f"{label}:{path}: external tree requires manual review")
            continue
        if oid not in cache:
            raw = git("cat-file", "blob", oid)
            cache[oid] = [] if b"\0" in raw else content_findings(raw.decode("utf-8", errors="replace"))
        for line, rule in cache[oid]:
            errors.append(f"{label}:{path}:{line}: {rule}")
    return errors


def commits_for_push(stream):
    tips = []
    for line in stream:
        fields = line.split()
        if not fields:
            continue
        if len(fields) != 4 or not re.fullmatch(r"[0-9a-fA-F]{40,64}", fields[1]):
            raise ValueError("invalid pre-push input")
        if set(fields[1]) != {"0"}:
            tips.append(fields[1])
    if not tips:
        return []
    return git("rev-list", *tips).decode().splitlines()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--staged", action="store_true", help="scan the index, not unstaged files")
    modes.add_argument("--history", action="store_true", help="scan commits reachable from all local refs")
    modes.add_argument("--pre-push", action="store_true", help="read proposed ref updates from standard input")
    args = parser.parse_args()
    cache, errors, warnings = {}, [], []
    try:
        if args.staged:
            errors += check_tree(git("write-tree").decode().strip(), "index", cache)
            count = 1
        else:
            commits = commits_for_push(sys.stdin) if args.pre_push else git("rev-list", "--all").decode().splitlines()
            count = len(commits)
            for commit in commits:
                label = commit[:12]
                errors += check_tree(commit, label, cache)
                message = git("show", "-s", "--format=%B", commit).decode("utf-8", errors="replace")
                for line, rule in content_findings(message):
                    errors.append(f"{label}:commit-message:{line}: {rule}")
                identities = git("show", "-s", "--format=%ae%x00%ce", commit).decode().strip().split("\0")
                if any(BAD_PLACEHOLDER.search(email) for email in identities):
                    warnings.append(f"{label}: local placeholder email; review author identity before publishing")
        for error in errors:
            print("BLOCK: " + error, file=sys.stderr)
        for warning in warnings:
            print("NOTE: " + warning, file=sys.stderr)
        if errors:
            print(f"Public-boundary check failed ({len(errors)} findings).", file=sys.stderr)
            return 1
        print(f"Public-boundary check passed ({count} indexed tree(s) or commit(s)).")
        return 0
    except (subprocess.CalledProcessError, ValueError) as error:
        print(f"Public-boundary check could not finish ({type(error).__name__}); inspect Git state.", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
