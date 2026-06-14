package repo

import "errors"

// ErrInvalidArgument marks domain validation failures that should map to HTTP 400.
var ErrInvalidArgument = errors.New("invalid argument")

// ErrNotFound marks missing rows that should map to HTTP 404.
var ErrNotFound = errors.New("not found")

// ErrForbidden marks an action the caller is not allowed to take even though
// they hold the route permission (e.g. approving one's own requisition/PO).
// Maps to HTTP 403.
var ErrForbidden = errors.New("forbidden")
