#!/usr/bin/env python3
"""Read-only retention planning. This program deliberately has no delete mode."""

import argparse
import collections
import datetime as dt
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import stat
import sys


def read_small(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError(f"not a regular file: {path}")
        data = stream.read((2 << 20) + 1)
    if len(data) > 2 << 20:
        raise ValueError(f"oversized metadata: {path}")
    return data


def footprint(root):
    size, files, links, newest = 0, 0, 0, root.stat().st_mtime
    for parent, dirs, names in os.walk(root, followlinks=False, onerror=raise_error):
        for name in dirs[:]:
            path = Path(parent) / name
            if path.is_symlink():
                links += 1
                dirs.remove(name)
        for name in names:
            info = (Path(parent) / name).lstat()
            newest = max(newest, info.st_mtime)
            if stat.S_ISREG(info.st_mode):
                size += info.st_size
                files += 1
            else:
                links += 1
    return {"bytes": size, "files": files, "special_entries": links}, newest


def raise_error(error):
    raise error


def metadata(path):
    value = json.loads(read_small(path))
    if not isinstance(value, dict):
        raise ValueError(f"metadata must be an object: {path}")
    return value


def inventory(root, success_days=None, failure_days=None, now=None):
    root = Path(root).absolute()
    if root.is_symlink() or not root.is_dir():
        raise ValueError("state root must be a real directory")
    for name in ("inbox", "wrk", "shell3.db"):
        if (root / name).is_symlink():
            raise ValueError(f"symlinked state entry: {name}")
    now = now or dt.datetime.now(dt.timezone.utc)
    with sqlite3.connect((root / "shell3.db").as_uri() + "?mode=ro", uri=True) as db:
        active = {row[0] for row in db.execute("SELECT id FROM schedule_runs WHERE status='running'")}
    pending = set()
    mailboxes = collections.Counter()
    for parent, dirs, files in os.walk(root / "inbox", followlinks=False, onerror=raise_error) if (root / "inbox").exists() else []:
        if any((Path(parent) / name).is_symlink() for name in dirs):
            raise ValueError("symlinked inbox directory; cannot establish pending references")
        if Path(parent).name not in ("new", "processing", "archived"):
            continue
        for name in files:
            if not name.endswith(".json"):
                continue
            message = metadata(Path(parent) / name)
            mailboxes[(message["to"], Path(parent).name)] += 1
            if Path(parent).name != "archived":
                if message.get("correlation"):
                    pending.add(message["correlation"])
                if message.get("to", "").startswith("wrk:"):
                    pending.add(message["to"].rsplit("/", 1)[-1])
    runs = []
    for task in sorted((root / "wrk").iterdir()) if (root / "wrk").exists() else []:
        if task.is_symlink() or not task.is_dir():
            raise ValueError(f"unexpected workflow entry: {task}")
        for path in sorted(task.iterdir()):
            if path.is_symlink() or not path.is_dir():
                raise ValueError(f"unexpected run entry: {path}")
            blockers = []
            lock = None
            try:
                lock = os.open(path / "beat.lock", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                if not stat.S_ISREG(os.fstat(lock).st_mode):
                    raise ValueError("invalid execution lock")
                try:
                    fcntl.flock(lock, fcntl.LOCK_SH | fcntl.LOCK_NB)
                except BlockingIOError:
                    blockers.append("execution_active")
                status = read_small(path / "status").decode().strip()
                manifest = metadata(path / "run.json")
                if manifest.get("version") != 1 or manifest.get("run_id") != path.name or manifest.get("task") != task.name:
                    raise ValueError("unknown or inconsistent run manifest")
                if status not in ("completed", "failed", "cancelled"):
                    blockers.append("nonterminal")
                if path.name in active:
                    blockers.append("schedule_running")
                if path.name in pending:
                    blockers.append("pending_notice")
                destination = manifest.get("notify_to")
                if status in ("failed", "cancelled"):
                    destination = manifest.get("notify_failure_to") or destination
                if destination:
                    receipt = metadata(path / "notify.json")
                    if receipt.get("persisted") is not True:
                        blockers.append("notice_not_confirmed")
                usage, newest = footprint(path)
                if usage["special_entries"]:
                    blockers.append("special_entries")
                age = max(0, (now.timestamp() - newest) / 86400)
                days = success_days if status == "completed" else failure_days
                if days is None:
                    blockers.append("retention_unset")
                elif age < days:
                    blockers.append("within_retention")
                runs.append({"run": f"{task.name}/{path.name}", "status": status,
                             "age_days": round(age, 2), **usage, "blockers": blockers,
                             "review_candidate": not blockers})
            except (OSError, ValueError, KeyError, TypeError) as error:
                runs.append({"run": f"{task.name}/{path.name}", "review_candidate": False,
                             "blockers": ["unreadable_or_unconfirmed_state"], "error": str(error)})
            finally:
                if lock is not None:
                    os.close(lock)
    return {"mode": "inventory_only", "observed_at": now.isoformat(),
            "warning": "Advisory live snapshot, never authorization to delete. Preserve ledger output references and recheck with all owners stopped.",
            "mailboxes": [{"destination": to, "status": status, "count": count}
                          for (to, status), count in sorted(mailboxes.items())],
            "runs": runs}


def positive_days(value):
    days = int(value)
    if days < 1:
        raise argparse.ArgumentTypeError("retention must be at least one day")
    return days


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("state_root", type=Path)
    parser.add_argument("--success-days", type=positive_days)
    parser.add_argument("--failure-days", type=positive_days)
    args = parser.parse_args()
    try:
        print(json.dumps(inventory(args.state_root, args.success_days, args.failure_days), indent=2))
    except (OSError, ValueError, KeyError, sqlite3.Error) as error:
        print(f"inventory failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
