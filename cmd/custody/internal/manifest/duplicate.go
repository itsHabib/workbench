package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// rejectDuplicateKeys fails closed if the manifest bytes carry the same object
// key twice at ANY nesting depth. encoding/json accepts duplicate keys silently
// (last value wins), so this walks the token stream itself — over the SAME bytes
// the loader decodes — and refuses the moment any object repeats a key. Scalars
// and well-formed structure need no attention here; malformed JSON is reported by
// the strict decode that follows, so a token error is passed through unchanged.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	return walkValue(dec)
}

// walkValue consumes exactly one JSON value (object, array, or scalar) from dec,
// recursing into containers. A non-delimiter token is a scalar and needs no walk.
func walkValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		return walkObject(dec)
	}
	if delim == '[' {
		return walkArray(dec)
	}
	return nil
}

// walkObject consumes an object whose opening brace was already read. It rejects
// the first repeated key it sees and recurses into each value. Within an object,
// encoding/json guarantees every key token is a string.
func walkObject(dec *json.Decoder) error {
	seen := make(map[string]bool)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("manifest: parse: non-string object key")
		}
		if seen[name] {
			return fmt.Errorf("%w: %q", ErrDuplicateField, name)
		}
		seen[name] = true
		if err := walkValue(dec); err != nil {
			return err
		}
	}
	_, err := dec.Token() // closing '}'
	return err
}

// walkArray consumes an array whose opening bracket was already read, recursing
// into each element so duplicate keys nested inside array elements are caught.
func walkArray(dec *json.Decoder) error {
	for dec.More() {
		if err := walkValue(dec); err != nil {
			return err
		}
	}
	_, err := dec.Token() // closing ']'
	return err
}
