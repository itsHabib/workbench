package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const magic = "EVENTQ01"
const maxIndex = 1 << 30

type storedColumn struct {
	Field
	Dictionary []string `json:"dictionary,omitempty"`
}
type header struct {
	Rows         int            `json:"rows"`
	BuiltAt      string         `json:"built_at"`
	Sources      []Source       `json:"sources"`
	IgnoredTails int            `json:"ignored_tails"`
	Columns      []storedColumn `json:"columns"`
}

// Save atomically creates a private index. It never replaces an existing path.
func Save(path string, t *Table) error {
	data, err := marshal(t)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".eventq-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Linking is an atomic no-clobber publication, including against symlinks.
	if err := os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("create index (choose a new output path): %w", err)
	}
	return nil
}

func marshal(t *Table) ([]byte, error) {
	h := header{Rows: t.Rows, BuiltAt: t.BuiltAt, Sources: t.Sources, IgnoredTails: t.IgnoredTails}
	for _, c := range t.Columns {
		h.Columns = append(h.Columns, storedColumn{c.Field, c.Dictionary})
	}
	meta, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	if len(meta) > 16<<20 || int64(t.Rows)*int64(len(t.Columns))*16+int64(len(meta))+44 > maxIndex {
		return nil, fmt.Errorf("index exceeds size limit; partition the input")
	}
	var b bytes.Buffer
	b.WriteString(magic)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(meta)))
	b.Write(meta)
	for _, c := range t.Columns {
		_ = binary.Write(&b, binary.LittleEndian, c.Values)
		_ = binary.Write(&b, binary.LittleEndian, c.Valid)
	}
	sum := sha256.Sum256(b.Bytes())
	b.Write(sum[:])
	return b.Bytes(), nil
}

// Load validates the format and checksum before allocating column arrays.
func Load(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxIndex {
		return nil, fmt.Errorf("index must be a regular file no larger than 1 GiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxIndex+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxIndex {
		return nil, fmt.Errorf("index exceeds 1 GiB")
	}
	return unmarshal(data)
}

func unmarshal(data []byte) (*Table, error) {
	if len(data) < 44 || string(data[:8]) != magic {
		return nil, fmt.Errorf("not an eventq v1 index")
	}
	payload := data[:len(data)-32]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], data[len(data)-32:]) {
		return nil, fmt.Errorf("index checksum mismatch")
	}
	n := int(binary.LittleEndian.Uint32(data[8:12]))
	if n > 16<<20 || n > len(payload)-12 {
		return nil, fmt.Errorf("invalid index header length")
	}
	var h header
	if err := json.Unmarshal(data[12:12+n], &h); err != nil {
		return nil, err
	}
	if h.Rows < 0 || h.Rows > 10_000_000 || len(h.Columns) < 3 || len(h.Columns) > 32 {
		return nil, fmt.Errorf("invalid index dimensions")
	}
	if int64(h.Rows)*int64(len(h.Columns))*16 != int64(len(payload)-12-n) {
		return nil, fmt.Errorf("index column lengths do not match header")
	}
	t := &Table{Rows: h.Rows, BuiltAt: h.BuiltAt, Sources: h.Sources, IgnoredTails: h.IgnoredTails}
	r := bytes.NewReader(payload[12+n:])
	seen := map[string]bool{}
	for _, col := range h.Columns {
		if !identifier(col.Name) || seen[col.Name] || (col.Type != "int" && col.Type != "string") {
			return nil, fmt.Errorf("invalid index column %q", col.Name)
		}
		seen[col.Name] = true
		c := Column{Field: col.Field, Dictionary: col.Dictionary, Values: make([]int64, h.Rows), Valid: make([]int64, h.Rows)}
		if err := binary.Read(r, binary.LittleEndian, c.Values); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.LittleEndian, c.Valid); err != nil {
			return nil, err
		}
		if err := validateColumn(&c); err != nil {
			return nil, err
		}
		t.Columns = append(t.Columns, c)
	}
	return t, nil
}

func validateColumn(c *Column) error {
	for i, valid := range c.Valid {
		if valid != 0 && valid != -1 {
			return fmt.Errorf("invalid presence mask in %s", c.Name)
		}
		if c.Type == "string" && valid != 0 && (c.Values[i] < 0 || c.Values[i] >= int64(len(c.Dictionary))) {
			return fmt.Errorf("invalid dictionary code in %s", c.Name)
		}
	}
	return nil
}
