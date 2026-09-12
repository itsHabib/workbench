package fleet

import (
	"bytes"
	"testing"
)

// The store is shared with the Python reference byte for byte, and json.dumps has one
// float repr. These are Python's own outputs for these values.
func TestDumpFloatIsPythonRepr(t *testing.T) {
	cases := map[float64]string{
		1788651458.341428: "1788651458.341428",
		5:                 "5.0",
		0:                 "0.0",
		1e17:              "1e+17",
		1e16:              "1e+16",
		1e15:              "1000000000000000.0",
		1e-7:              "1e-07",
		0.1:               "0.1",
		123456789012.5:    "123456789012.5",
		0.0001:            "0.0001",
		0.00001:           "1e-05",
		-2.5:              "-2.5",
		1.5e17:            "1.5e+17",
	}
	for f, want := range cases {
		var b bytes.Buffer
		dumpFloat(&b, f)
		if b.String() != want {
			t.Errorf("dumpFloat(%v) = %q, want %q", f, b.String(), want)
		}
	}
}
