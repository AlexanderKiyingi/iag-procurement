package notifications

// AlertJobPayload is stored in notification_email_jobs.payload and sent to the HTML template.
type AlertJobPayload struct {
	To      []string `json:"to"`
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Detail  string   `json:"detail,omitempty"`
	// Audience names a desk ("approvals.procurement") instead of listing
	// addresses. When set, To is the fallback used until an administrator has
	// routed that audience. Either is sufficient.
	Audience string `json:"audience,omitempty"`
}
