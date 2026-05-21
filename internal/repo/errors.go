package repo

import "errors"

// ErrInvalidArgument marks domain validation failures that should map to HTTP 400.
var ErrInvalidArgument = errors.New("invalid argument")

// ErrNotFound marks missing rows that should map to HTTP 404.
var ErrNotFound = errors.New("not found")
