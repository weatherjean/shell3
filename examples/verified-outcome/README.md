# Verified application outcomes

Keep business outcomes in workflow artifacts. Both `inserted` and a legitimate
`skipped` outcome can complete a workflow; an infrastructure or verification
failure fails its check and may consume another loop attempt.

`verify_outcome.py` is a standard-library Python example with a deliberately
small extension seam: the application supplies `verify_insert(receipt)`. That
callback opens its database read-only, checks the exact ID and deduplication
hash, and verifies that the record belongs to this run. Return `True` only when
all checks pass. A durable run ID stored with the application row is preferable;
an older schema needs an explicit, documented provenance check. Do not create a
new insertion in the verifier or accept a matching record from an older run.

The worker writes `report-$TASK_ATTEMPT.md`, then
`outcome-$TASK_ATTEMPT.json`, for example:

```json
{"run":"CURRENT_TASK_RUN","attempt":1,"outcome":"inserted","id":42,"dedup_hash":"APPLICATION_HASH","report_sha256":"SHA256_OF_REPORT_BYTES"}
```

A skip replaces `id` and `dedup_hash` with `reason`. The application supplies a
closed list of legitimate skip reasons. Database failures, unavailable required
sources, and failed validation must not be converted into editorial skips.

The application's check calls:

```python
outcome, report = verify(
    os.environ["TASK_ARTIFACTS"], os.environ["TASK_RUN"],
    int(os.environ["TASK_ATTEMPT"]), verify_insert,
    {"no_qualifying_evidence", "duplicate"},
)
```

Only after that succeeds, atomically write the returned report bytes to the
schedule's required output path. Wire the application check through the existing
workflow boundary:

```lisp
(until (sh "python3 scripts/check_outcome.py"))
```

No global `skipped` workflow state is needed. The report identifies the business
outcome; the workflow state says whether the declared acceptance check passed.
Run/attempt fields and the report digest reject stale or mismatched artifacts.
They do not establish authorship against a malicious process that can rewrite
receipts or application data. shell3 is not an OS sandbox.

Test the application callback against a temporary database: exact match, missing
row, stale row, wrong hash, unreadable database, and duplicate insertion. Test
recovery after insertion but before receipt/report persistence. Retrying should
recover that run's existing row, never insert a second row merely because the
previous attempt's reporting failed.
