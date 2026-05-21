package notifications

// AlertJobPayload is stored in notification_email_jobs.payload and sent to the HTML template.
type AlertJobPayload struct {
	To      []string `json:"to"`
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Detail  string   `json:"detail,omitempty"`
}
