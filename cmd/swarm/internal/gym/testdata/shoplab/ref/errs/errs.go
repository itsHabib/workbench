package errs

import "errors"

var (
	ErrInvalid      = errors.New("invalid")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInsufficient = errors.New("insufficient")
	ErrState        = errors.New("bad state")
)

func Code(err error) string {
	switch {
	case err == nil:
		return "OK"
	case errors.Is(err, ErrInvalid):
		return "INVALID"
	case errors.Is(err, ErrNotFound):
		return "NOT_FOUND"
	case errors.Is(err, ErrConflict):
		return "CONFLICT"
	case errors.Is(err, ErrInsufficient):
		return "INSUFFICIENT"
	case errors.Is(err, ErrState):
		return "STATE"
	}
	return "INTERNAL"
}
