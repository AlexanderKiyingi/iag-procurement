package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-platform-go/objectstore"

	"iag-procurement/backend/internal/middleware"
)

// Attachments for procurement records (supplier invoices, delivery notes, GRN
// evidence) live in the shared public.attachments table, keyed by owner_ref -
// the record's own reference, e.g. an invoice_no like BILL-0584.
//
// Bytes never pass through this service. Upload returns a presigned PUT URL and
// the browser sends the file straight to the bucket; download returns a
// presigned GET. That keeps large files off the request path and means an
// upload does not occupy a service goroutine for its duration.
//
// The metadata row is written BEFORE the object exists, with storage_key set.
// The alternative - write the row after a successful upload - needs the client
// to make a second call it may never make, which loses the file silently. A row
// whose object is missing is visible and fixable; an object nobody recorded is
// not.

const attachmentURLExpiry = 15 * time.Minute

type attachmentCreateRequest struct {
	OwnerType string `json:"ownerType"`
	OwnerRef  string `json:"ownerRef"`
	Filename  string `json:"filename"`
	Mime      string `json:"mime"`
	SizeBytes int64  `json:"sizeBytes"`
}

type attachmentResponse struct {
	ID         string `json:"id"`
	OwnerType  string `json:"ownerType"`
	OwnerRef   string `json:"ownerRef"`
	Filename   string `json:"filename"`
	Mime       string `json:"mime"`
	SizeBytes  int64  `json:"sizeBytes"`
	UploadedBy string `json:"uploadedBy,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UploadURL  string `json:"uploadUrl,omitempty"`
	Pending    bool   `json:"pending"`
}

// postAttachment records an attachment and returns a presigned PUT URL.
func (a *API) postAttachment(c *gin.Context) {
	if a.files == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "STORAGE_UNCONFIGURED",
			"message": "object storage is not configured; set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY",
		}})
		return
	}
	var req attachmentCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_BODY", "message": err.Error()}})
		return
	}
	req.OwnerRef = strings.TrimSpace(req.OwnerRef)
	req.Filename = strings.TrimSpace(req.Filename)
	if req.OwnerRef == "" || req.Filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "INVALID_BODY", "message": "ownerRef and filename are required"}})
		return
	}
	if req.OwnerType == "" {
		req.OwnerType = "invoice"
	}
	if req.Mime == "" {
		req.Mime = "application/octet-stream"
	}

	id := uuid.NewString()
	// Namespaced by service and owner so keys stay readable in the bucket and
	// two services cannot collide on one.
	key := fmt.Sprintf("procurement/%s/%s/%s", req.OwnerType, req.OwnerRef, id)

	_, err := a.pool.Exec(c.Request.Context(), `
		INSERT INTO public.attachments
		    (id, owner_service, owner_type, owner_ref, filename, mime, size_bytes,
		     storage_key, uploaded_by, uploaded_at, created_at)
		VALUES ($1, 'procurement', $2, $3, $4, $5, $6, $7, $8, now(), now())`,
		id, req.OwnerType, req.OwnerRef, req.Filename, req.Mime, req.SizeBytes,
		key, principalEmail(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "DB_ERROR", "message": err.Error()}})
		return
	}

	c.JSON(http.StatusCreated, attachmentResponse{
		ID: id, OwnerType: req.OwnerType, OwnerRef: req.OwnerRef,
		Filename: req.Filename, Mime: req.Mime, SizeBytes: req.SizeBytes,
		UploadedBy: principalEmail(c), CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UploadURL: a.files.PresignPut(key, attachmentURLExpiry),
		Pending:   true,
	})
}

// listAttachments returns the attachments recorded against one owner reference.
func (a *API) listAttachments(c *gin.Context) {
	ref := strings.TrimSpace(c.Query("ownerRef"))
	if ref == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "INVALID_QUERY", "message": "ownerRef is required"}})
		return
	}
	// ownerType is optional and narrows the result. Two record kinds can carry
	// the same reference - a purchase order and the invoice raised against it
	// are both addressed by an id the caller chose - so a client that knows
	// which kind it is asking about should be able to say so rather than
	// filtering the other kind out on the far side.
	ownerType := strings.TrimSpace(c.Query("ownerType"))
	rows, err := a.pool.Query(c.Request.Context(), `
		SELECT id::text, owner_type, owner_ref, filename, mime, size_bytes,
		       coalesce(uploaded_by,''), created_at, storage_key IS NULL
		  FROM public.attachments
		 WHERE owner_service = 'procurement' AND owner_ref = $1
		   AND ($2 = '' OR owner_type = $2)
		 ORDER BY created_at DESC`, ref, ownerType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "DB_ERROR", "message": err.Error()}})
		return
	}
	defer rows.Close()

	out := []attachmentResponse{}
	for rows.Next() {
		var r attachmentResponse
		var created time.Time
		// pending here means "no storage_key recorded" - the migrated rows are
		// in that state until the backfill uploads their bytes.
		if err := rows.Scan(&r.ID, &r.OwnerType, &r.OwnerRef, &r.Filename, &r.Mime,
			&r.SizeBytes, &r.UploadedBy, &created, &r.Pending); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "DB_ERROR", "message": err.Error()}})
			return
		}
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	c.JSON(http.StatusOK, gin.H{"attachments": out})
}

// getAttachmentURL returns a short-lived download URL for one attachment.
func (a *API) getAttachmentURL(c *gin.Context) {
	if a.files == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "STORAGE_UNCONFIGURED", "message": "object storage is not configured"}})
		return
	}
	var key, filename string
	err := a.pool.QueryRow(c.Request.Context(), `
		SELECT coalesce(storage_key,''), filename FROM public.attachments
		 WHERE id = $1 AND owner_service = 'procurement'`, c.Param("id")).Scan(&key, &filename)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "NOT_FOUND", "message": "attachment not found"}})
		return
	}
	if key == "" {
		// Distinguishable on purpose: the record exists but its bytes were never
		// uploaded to the bucket. Saying "not found" here would hide a backlog.
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code":    "NOT_UPLOADED",
			"message": "this attachment has no object yet; its bytes are still awaiting migration to object storage",
		}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"filename": filename, "url": a.files.PresignGet(key, attachmentURLExpiry)})
}

// deleteAttachment removes one attachment: the object first, then the row.
//
// That order is deliberate, and it is the opposite of the create path. On
// create the row is written before the object exists, so a failure leaves a
// visible record whose bytes are missing. On delete the object goes first, so a
// failure leaves a visible record whose bytes are gone - both are states an
// operator can see and act on. Deleting the row first would leave an object
// nobody has a reference to: invisible, unbilled to any record, and impossible
// to find again.
//
// A missing object is not an error. The store's Delete is idempotent, and rows
// awaiting the storage backfill have no object at all; refusing to delete those
// would strand exactly the rows most likely to need cleaning up.
func (a *API) deleteAttachment(c *gin.Context) {
	if a.files == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "STORAGE_UNCONFIGURED",
			"message": "object storage is not configured",
		}})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var key string
	err := a.pool.QueryRow(c.Request.Context(), `
		SELECT coalesce(storage_key,'') FROM public.attachments
		 WHERE id = $1 AND owner_service = 'procurement'`, id).Scan(&key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"code": "NOT_FOUND", "message": "attachment not found"}})
		return
	}
	if key != "" {
		if err := a.files.Delete(key); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
				"code":    "STORAGE_ERROR",
				"message": "could not remove the stored file: " + err.Error(),
			}})
			return
		}
	}
	if _, err := a.pool.Exec(c.Request.Context(), `
		DELETE FROM public.attachments
		 WHERE id = $1 AND owner_service = 'procurement'`, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": err.Error()}})
		return
	}
	c.Status(http.StatusNoContent)
}

// principalEmail returns the authenticated caller's email, or "" when the route
// is reached without one. It is recorded for provenance only, so an empty value
// is not an error.
func principalEmail(c *gin.Context) string {
	if v, ok := c.Get(middleware.CtxEmail); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// presignPutter is the subset of the store these handlers need, so the API
// struct does not depend on the concrete S3 type.
type presignPutter interface {
	PresignPut(key string, expiry time.Duration) string
	PresignGet(key string, expiry time.Duration) string
	Delete(key string) error
}

var _ presignPutter = (*objectstore.S3Store)(nil)
