package app

import "errors"

// ErrNotFound is a storage-independent read failure exposed by repository ports.
var ErrNotFound = errors.New("not found")
