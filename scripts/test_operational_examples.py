import datetime as dt
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest

from retention_inventory import inventory

spec = importlib.util.spec_from_file_location("verify_outcome", Path(__file__).resolve().parents[1] / "examples/verified-outcome/verify_outcome.py")
outcomes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(outcomes)


class OutcomeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.report = b"A useful report.\n"
        (self.root / "report-2.md").write_bytes(self.report)
        self.receipt = {"run": "this-run", "attempt": 2, "outcome": "inserted", "id": 4,
                        "dedup_hash": "row-hash", "report_sha256": hashlib.sha256(self.report).hexdigest()}

    def verify(self, check=lambda receipt: True):
        (self.root / "outcome-2.json").write_text(json.dumps(self.receipt))
        return outcomes.verify(self.root, "this-run", 2, check, {"no_qualifying_evidence", "duplicate"})

    def test_insert_requires_independent_confirmation(self):
        self.assertEqual(self.verify(), ("inserted", self.report))
        for result in (False, None, 1):
            with self.assertRaises(ValueError):
                self.verify(lambda receipt: result)

    def test_editorial_skip_is_success_but_failure_is_not(self):
        self.receipt.pop("id")
        self.receipt.pop("dedup_hash")
        self.receipt.update(outcome="skipped", reason="no_qualifying_evidence")
        self.assertEqual(self.verify(), ("skipped", self.report))
        self.receipt["reason"] = "database_unavailable"
        with self.assertRaises(ValueError):
            self.verify()
        self.receipt.update(outcome="failed", reason="no_qualifying_evidence")
        with self.assertRaises(ValueError):
            self.verify()

    def test_stale_and_malformed_receipts(self):
        original = self.receipt.copy()
        for field, value in (("run", "old-run"), ("attempt", 1), ("attempt", True), ("id", True), ("outcome", "other"), ("extra", 1), ("report_sha256", "wrong")):
            with self.subTest(field=field, value=value):
                self.receipt = {**original, field: value}
                with self.assertRaises(ValueError):
                    self.verify()

    def test_missing_empty_and_symlinked_report(self):
        path = self.root / "report-2.md"
        path.unlink()
        with self.assertRaises(OSError):
            self.verify()
        path.write_text(" \n")
        self.receipt["report_sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
        with self.assertRaises(ValueError):
            self.verify()
        path.unlink()
        (self.root / "other").write_bytes(self.report)
        path.symlink_to(self.root / "other")
        with self.assertRaises(OSError):
            self.verify()


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        with sqlite3.connect(self.root / "shell3.db") as db:
            db.execute("CREATE TABLE schedule_runs(id TEXT, status TEXT)")
        self.run = self.root / "wrk/task/run"
        self.run.mkdir(parents=True)
        (self.run / "beat.lock").touch()
        (self.run / "status").write_text("completed\n")
        (self.run / "run.json").write_text(json.dumps({"version": 1, "task": "task", "run_id": "run"}))
        self.now = dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=100)

    def inspect(self, days=30):
        return inventory(self.root, days, days, self.now)["runs"][0]

    def test_dry_run_does_not_modify_and_requires_explicit_age(self):
        before = {p: p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        self.assertFalse(self.inspect(None)["review_candidate"])
        self.assertTrue(self.inspect()["review_candidate"])
        after = {p: p.read_bytes() for p in self.root.rglob("*") if p.is_file()}
        self.assertEqual(before, after)

    def test_active_nonterminal_and_pending_are_protected(self):
        with (self.run / "beat.lock").open() as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.assertIn("execution_active", self.inspect()["blockers"])
        (self.run / "status").write_text("interrupted\n")
        self.assertIn("nonterminal", self.inspect()["blockers"])
        (self.run / "status").write_text("completed\n")
        with sqlite3.connect(self.root / "shell3.db") as db:
            db.execute("INSERT INTO schedule_runs VALUES('run','running')")
        self.assertIn("schedule_running", self.inspect()["blockers"])
        pending = self.root / "inbox/mailbox/new"
        pending.mkdir(parents=True)
        (pending / "notice.json").write_text(json.dumps({"to": "silent", "correlation": "run"}))
        self.assertIn("pending_notice", self.inspect()["blockers"])

    def test_symlinks_and_incomplete_metadata_fail_closed(self):
        (self.run / "artifact").symlink_to(self.root / "shell3.db")
        self.assertIn("special_entries", self.inspect()["blockers"])
        (self.run / "beat.lock").unlink()
        self.assertFalse(self.inspect()["review_candidate"])
        (self.root / "wrk/linked").symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(ValueError):
            self.inspect()

    def test_unconfirmed_notices_and_unknown_state_are_protected(self):
        manifest = self.run / "run.json"
        data = json.loads(manifest.read_text())
        data["notify_to"] = "main"
        manifest.write_text(json.dumps(data))
        self.assertFalse(self.inspect()["review_candidate"])
        (self.run / "notify.json").write_text('{"persisted":false}')
        self.assertIn("notice_not_confirmed", self.inspect()["blockers"])
        (self.run / "notify.json").write_text('{"persisted":true}')
        self.assertTrue(self.inspect()["review_candidate"])
        data["version"] = 999
        manifest.write_text(json.dumps(data))
        self.assertFalse(self.inspect()["review_candidate"])

    def test_missing_db_is_not_created(self):
        (self.root / "shell3.db").unlink()
        with self.assertRaises(sqlite3.Error):
            self.inspect()
        self.assertFalse((self.root / "shell3.db").exists())


if __name__ == "__main__":
    unittest.main()
