"""A workflow check, not a shell3 built-in. Supply an application DB verifier."""

import hashlib
import json
import os
from pathlib import Path
import stat


def regular_bytes(path, limit):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError("acceptance input must be a regular file")
        data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError("acceptance input exceeds its size limit")
    return data


def verify(artifacts, run, attempt, verify_insert, skip_reasons):
    """Return (outcome, report bytes) only after all acceptance checks pass.

    verify_insert(receipt) must independently check persisted application data
    and raise on any mismatch. It must not trust the worker's insertion claim.
    A run/attempt match prevents accidental reuse; it is not proof of authorship
    against a malicious worker with write access to the application database.
    """
    if not run or type(attempt) is not int or attempt < 1:
        raise ValueError("a current run and positive attempt are required")
    artifacts = Path(artifacts)
    if artifacts.is_symlink() or not artifacts.is_dir():
        raise ValueError("artifacts must be a real directory")
    receipt = json.loads(regular_bytes(artifacts / f"outcome-{attempt}.json", 16384))
    if not isinstance(receipt, dict):
        raise ValueError("receipt must be an object")
    common = {"run", "attempt", "outcome", "report_sha256"}
    outcome = receipt.get("outcome")
    extra = {"id", "dedup_hash"} if outcome == "inserted" else {"reason"}
    if set(receipt) != common | extra:
        raise ValueError("unknown or missing receipt fields")
    if receipt["run"] != run or type(receipt["attempt"]) is not int or receipt["attempt"] != attempt:
        raise ValueError("receipt belongs to a different run or attempt")
    report = regular_bytes(artifacts / f"report-{attempt}.md", 1 << 20)
    if not report.decode("utf-8").strip():
        raise ValueError("a nonempty UTF-8 report is required")
    if receipt["report_sha256"] != hashlib.sha256(report).hexdigest():
        raise ValueError("report does not match receipt")
    if outcome == "inserted":
        if type(receipt["id"]) is not int or receipt["id"] < 1 or not isinstance(receipt["dedup_hash"], str) or not receipt["dedup_hash"]:
            raise ValueError("insertion receipt requires an ID and deduplication hash")
        if verify_insert(receipt) is not True:
            raise ValueError("insertion was not independently confirmed")
    elif outcome == "skipped":
        if not isinstance(receipt["reason"], str) or receipt["reason"] not in skip_reasons:
            raise ValueError("skip reason is not an accepted editorial outcome")
    else:
        raise ValueError("worker failed or supplied an unknown outcome")
    return outcome, report
