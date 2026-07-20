package rooms

import (
	"bytes"
	"io"
	"sort"
)

const redactToken = "[REDACTED]"

type redactor struct {
	dst     io.Writer
	secrets [][]byte
	tail    []byte
	maxTail int
}

func newRedactor(dst io.Writer, secrets [][]byte) *redactor {
	kept := make([][]byte, 0, len(secrets))
	maxTail := 0
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		kept = append(kept, secret)
		if len(secret)-1 > maxTail {
			maxTail = len(secret) - 1
		}
	}
	sort.Slice(kept, func(i, j int) bool { return len(kept[i]) > len(kept[j]) })
	return &redactor{dst: dst, secrets: kept, maxTail: maxTail}
}

func (r *redactor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := append(r.tail, p...)
	for _, secret := range r.secrets {
		buf = bytes.ReplaceAll(buf, secret, []byte(redactToken))
	}
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

func (r *redactor) Close() error {
	if len(r.tail) == 0 {
		return nil
	}
	_, err := r.dst.Write(r.tail)
	r.tail = nil
	return err
}
