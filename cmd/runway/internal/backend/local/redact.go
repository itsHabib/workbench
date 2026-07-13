package local

import (
	"bytes"
	"io"
)

const redactToken = "[REDACTED]"

// redactor replaces exact resolved secret byte sequences with [REDACTED]
// before persistence, including matches split across read chunks (D8).
type redactor struct {
	dst     io.Writer
	secrets [][]byte
	tail    []byte
	maxTail int
}

func newRedactor(dst io.Writer, secrets [][]byte) *redactor {
	maxTail := 0
	kept := make([][]byte, 0, len(secrets))
	for _, s := range secrets {
		if len(s) == 0 {
			continue
		}
		kept = append(kept, s)
		if len(s)-1 > maxTail {
			maxTail = len(s) - 1
		}
	}
	return &redactor{dst: dst, secrets: kept, maxTail: maxTail}
}

func (r *redactor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := append(r.tail, p...)
	buf = replaceSecrets(buf, r.secrets)
	if r.maxTail == 0 {
		r.tail = nil
		if _, err := r.dst.Write(buf); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if len(buf) <= r.maxTail {
		r.tail = buf
		return len(p), nil
	}
	safe := len(buf) - r.maxTail
	if _, err := r.dst.Write(buf[:safe]); err != nil {
		return 0, err
	}
	r.tail = append([]byte(nil), buf[safe:]...)
	return len(p), nil
}

// Close flushes any retained tail that cannot complete a secret match.
func (r *redactor) Close() error {
	if len(r.tail) == 0 {
		return nil
	}
	_, err := r.dst.Write(r.tail)
	r.tail = nil
	return err
}

func replaceSecrets(buf []byte, secrets [][]byte) []byte {
	for _, s := range secrets {
		buf = bytes.ReplaceAll(buf, s, []byte(redactToken))
	}
	return buf
}
