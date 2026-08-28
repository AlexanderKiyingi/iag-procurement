// Command backfill-attachments uploads migrated attachment bytes into object
// storage and stamps the storage_key that makes them downloadable.
//
// It lives inside procurement rather than as a standalone tool because the S3
// credentials are configured per service in Railway and are not available on a
// developer machine. Running it here means it inherits DATABASE_URL and the S3_*
// variables the service already has - the same reason cmd/migrate lives beside
// the service rather than outside it.
//
// It is NOT procurement-specific: it backfills every owner_service in
// public.attachments, because the corpus is shared and the bytes all sit in the
// one staging table. Use -service to narrow it.
//
// ORIGINAL HEADER FOLLOWS.
//
// Command backfill-attachments uploads migrated attachment bytes into object
// storage and stamps the storage_key that makes them downloadable.
//
// The 33 attachments cloned from the monolith landed in public.attachments with
// storage_key NULL and source_blob_id pointing at legacy_erp.attachment_blobs.
// That was deliberate: the object store was not connected yet, and recording a
// key that resolves to nothing is worse than recording none - a null says "not
// uploaded" and is findable, a dangling key says "uploaded" and is not.
//
// This closes that gap. It is idempotent: rows that already have a storage_key
// are skipped, so a re-run after a partial failure resumes rather than
// re-uploading.
//
// ==================== ORDERING ====================
//
// Upload first, verify, and only then stamp the key. Stamping before the upload
// succeeds would produce exactly the dangling key the null was avoiding.
//
// Usage:
//
//	DATABASE_URL=postgres://... \
//	S3_ENDPOINT=... S3_BUCKET=... S3_ACCESS_KEY_ID=... S3_SECRET_ACCESS_KEY=... \
//	go run .
//
//	go run . -dry-run     report what would move, change nothing
//	go run . -service finance   limit to one owner_service
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alvor-technologies/iag-platform-go/objectstore"
)

type pending struct {
	ID           string
	OwnerService string
	OwnerType    string
	OwnerRef     string
	Filename     string
	Mime         string
	Checksum     string
	Bytes        []byte
}

func main() {
	var (
		dryRun  = flag.Bool("dry-run", false, "report what would be uploaded and change nothing")
		service = flag.String("service", "", "limit to one owner_service (procurement, finance, contract-management, unassigned)")
	)
	flag.Parse()

	if err := run(*dryRun, *service); err != nil {
		log.Fatalf("backfill: %v", err)
	}
}

func run(dryRun bool, service string) error {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}

	store := objectstore.NewS3Store(
		os.Getenv("S3_ENDPOINT"), envOr("S3_REGION", "auto"), os.Getenv("S3_BUCKET"),
		os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY"),
		!strings.EqualFold(envOr("S3_USE_SSL", "true"), "false"),
	)
	if store == nil && !dryRun {
		return errors.New("object storage is not configured: set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY (or pass -dry-run)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT a.id::text, a.owner_service, a.owner_type, a.owner_ref, a.filename,
		       a.mime, coalesce(a.checksum,''), b.bytes
		  FROM public.attachments a
		  JOIN legacy_erp.attachment_blobs b ON b.id::text = a.source_blob_id::text
		 WHERE a.storage_key IS NULL
		   AND ($1 = '' OR a.owner_service = $1)
		 ORDER BY a.owner_service, a.created_at`, service)
	if err != nil {
		return fmt.Errorf("select pending: %w", err)
	}

	var work []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.ID, &p.OwnerService, &p.OwnerType, &p.OwnerRef,
			&p.Filename, &p.Mime, &p.Checksum, &p.Bytes); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		work = append(work, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(work) == 0 {
		log.Printf("nothing pending: every attachment already has a storage_key")
		return nil
	}

	var total int
	for _, p := range work {
		total += len(p.Bytes)
	}
	log.Printf("pending: %d attachments, %.1f KB", len(work), float64(total)/1024)

	if dryRun {
		for _, p := range work {
			log.Printf("  would upload %-20s %-24s %-40s %6d B -> %s",
				p.OwnerService, p.OwnerRef, truncate(p.Filename, 40), len(p.Bytes), objectKey(p))
		}
		log.Printf("dry run: nothing uploaded, nothing stamped")
		return nil
	}

	var done, failed int
	for _, p := range work {
		key := objectKey(p)

		// The checksum came from the source. Verifying before upload catches a
		// blob that was corrupted in staging, rather than faithfully copying
		// damage into the bucket and stamping it as good.
		if p.Checksum != "" {
			sum := sha256.Sum256(p.Bytes)
			if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, p.Checksum) {
				log.Printf("  SKIP %s (%s): checksum mismatch, source says %s, bytes are %s",
					p.ID, p.Filename, short(p.Checksum), short(got))
				failed++
				continue
			}
		}

		if _, err := store.Put(key, p.Mime, bytes.NewReader(p.Bytes)); err != nil {
			log.Printf("  FAIL %s (%s): %v", p.ID, p.Filename, err)
			failed++
			continue
		}

		// Only now is the key true. A failure here leaves an orphaned object in
		// the bucket, which is recoverable; stamping before a successful upload
		// would leave a row pointing at nothing, which is not.
		if _, err := pool.Exec(ctx,
			`UPDATE public.attachments SET storage_key = $2 WHERE id = $1 AND storage_key IS NULL`,
			p.ID, key); err != nil {
			log.Printf("  FAIL %s: uploaded but could not stamp key: %v", p.ID, err)
			failed++
			continue
		}
		done++
	}

	log.Printf("uploaded and stamped %d, failed %d, of %d pending", done, failed, len(work))
	if failed > 0 {
		return fmt.Errorf("%d attachment(s) did not complete; re-run to retry only those", failed)
	}
	return nil
}

// objectKey mirrors the layout the services use when they upload:
// <service>/<ownerType>/<ownerRef>/<id>. Keys stay readable in the bucket and
// two services cannot collide on one.
func objectKey(p pending) string {
	svc := p.OwnerService
	if svc == "" {
		svc = "unassigned"
	}
	return fmt.Sprintf("%s/%s/%s/%s", svc, p.OwnerType, p.OwnerRef, p.ID)
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
