package fleet

import "testing"

// Within is the one containment test every presence view uses. It must be by path
// component and must accept either separator, because CanonPath keeps the platform's
// and a Windows descendant is `c:\pool\slot\src`, never `c:\pool\slot/src`.
func TestWithinIsByComponentOnEitherSeparator(t *testing.T) {
	cases := []struct {
		p, dir string
		want   bool
	}{
		{"/pool/slot", "/pool/slot", true},
		{"/pool/slot/", "/pool/slot", true},
		{"/pool/slot/src", "/pool/slot", true},
		{"/pool/slot/src/deep", "/pool/slot/", true},
		{"/pool/slot-1", "/pool/slot", false},   // a string prefix is not an ancestor
		{"/pool/slotsrc", "/pool/slot", false},
		{"/pool", "/pool/slot", false},          // the parent is not within the child
		{`c:\pool\slot\src`, `c:\pool\slot`, true},
		{`c:\pool\slot`, `c:\pool\slot\`, true},
		{`c:\pool\slot-1`, `c:\pool\slot`, false},
		{`c:\pool\slot/src`, `c:\pool\slot`, true}, // mixed, as a shell on Windows produces
	}
	for _, c := range cases {
		if got := Within(c.p, c.dir); got != c.want {
			t.Errorf("Within(%q, %q) = %v, want %v", c.p, c.dir, got, c.want)
		}
	}
}
