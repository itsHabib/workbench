package errs

import "errors"

var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrState    = errors.New("wrong state")
	ErrExpired  = errors.New("expired")
	ErrCycle    = errors.New("cycle")
)

var codes = []struct {
	err  error
	code string
}{
	{ErrInvalid, "INVALID"},
	{ErrNotFound, "NOT_FOUND"},
	{ErrConflict, "CONFLICT"},
	{ErrState, "STATE"},
	{ErrExpired, "EXPIRED"},
	{ErrCycle, "CYCLE"},
}

func Code(err error) string {
	if err == nil {
		return "OK"
	}
	for _, c := range codes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return "INTERNAL"
}
