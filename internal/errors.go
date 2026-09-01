package internal

import "errors"

// ErrNotFound is returned when a tag, rule, or assignment does not exist.
var ErrNotFound = errors.New("not found")

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrNotFound)
}
