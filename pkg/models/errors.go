package models

import "errors"

// ErrUnavailable means a data store could not be reached; the request may
// succeed if retried later.
var ErrUnavailable = errors.New("data store unavailable")
