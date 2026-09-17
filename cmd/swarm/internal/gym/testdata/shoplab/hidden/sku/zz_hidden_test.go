package sku

import (
	"errors"
	"reflect"
	"testing"

	"shoplab/errs"
)

func TestHiddenParseCanonical(t *testing.T) {
	for in, want := range map[string]SKU{"abc": "ABC", "  tea-001\t": "TEA-001", "A1B2C3D4E5F6G7H8": "A1B2C3D4E5F6G7H8", "x-9": "X-9"} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestHiddenParseRejects(t *testing.T) {
	for _, in := range []string{"", "ab", "A1B2C3D4E5F6G7H8I", "-abc", "abc-", "ab c", "ab_c", "abç", "a.b.c"} {
		if got, err := Parse(in); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Parse(%q) = %q, %v; want ErrInvalid", in, got, err)
		}
	}
}

func TestHiddenMustParse(t *testing.T) {
	if got := MustParse("mug-1"); got != "MUG-1" || got.String() != "MUG-1" {
		t.Fatalf("got %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustParse did not panic")
		}
	}()
	MustParse("no")
}

func TestHiddenSort(t *testing.T) {
	s := []SKU{"TEA-2", "MUG", "TEA-10", "ABC"}
	Sort(s)
	if !reflect.DeepEqual(s, []SKU{"ABC", "MUG", "TEA-10", "TEA-2"}) {
		t.Fatalf("got %v", s)
	}
	Sort(nil)
}
