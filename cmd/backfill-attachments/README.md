# backfill-attachments

Uploads the attachment bytes cloned from the ERP monolith into object storage and
stamps the `storage_key` that makes them downloadable.

## Why it lives here

The S3 credentials are configured per service in Railway and are not available on
a developer machine, so a standalone tool cannot reach the bucket. Running it as
a command inside procurement means it inherits `DATABASE_URL` and the `S3_*`
variables the service already has — the same reason `cmd/migrate` lives beside
the service rather than outside it.

It is **not** procurement-specific: it backfills every `owner_service` in
`public.attachments`, because the corpus is shared and the bytes all sit in one
staging table. Use `-service` to narrow it.

## Running it

From a shell with the service's environment (`railway run`, or a one-off command
on the service):

```bash
go run ./cmd/backfill-attachments -dry-run   # report the plan, change nothing
go run ./cmd/backfill-attachments            # upload and stamp
go run ./cmd/backfill-attachments -service finance
```

`-dry-run` needs only `DATABASE_URL`; the real run needs the S3 variables too.

## Behaviour worth knowing

- **Idempotent.** Rows that already have a `storage_key` are skipped, so a re-run
  after a partial failure resumes rather than re-uploading.
- **Upload, verify, then stamp — in that order.** Stamping first would produce a
  key that resolves to nothing, which is worse than no key: a null says "not
  uploaded" and is findable through `idx_attachments_pending`, a dangling key
  says "uploaded" and is not. A failure between upload and stamp leaves an
  orphaned object, which is recoverable; the reverse is not.
- **Checksums are verified before upload.** Each row carries a sha256 from the
  source; a mismatch is skipped and named rather than copying corrupted bytes
  into the bucket and stamping them as good.
- **Exit code is non-zero if any attachment failed**, and the failures are named.

Keys are `<service>/<ownerType>/<ownerRef>/<id>` — identical to what the
procurement and finance handlers generate, so a file placed here is found by the
API and vice versa.

## Expected result

33 attachments, ~5.5 MB: procurement 13, contract-management 10, finance 7,
unassigned 3. Afterwards
`SELECT count(*) FROM public.attachments WHERE storage_key IS NULL` should be 0.
