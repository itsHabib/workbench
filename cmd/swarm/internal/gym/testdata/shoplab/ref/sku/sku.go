package sku

import (
	"fmt"
	"sort"
	"strings"

	"shoplab/errs"
)

type SKU string

func Parse(s string) (SKU, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 3 || len(s) > 16 || s[0] == '-' || s[len(s)-1] == '-' {
		return "", fmt.Errorf("sku: %q: %w", s, errs.ErrInvalid)
	}
	for _, c := range s {
		ok := c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
		if !ok {
			return "", fmt.Errorf("sku: %q: %w", s, errs.ErrInvalid)
		}
	}
	return SKU(s), nil
}

func MustParse(s string) SKU {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

func (s SKU) String() string { return string(s) }

func Sort(s []SKU) { sort.Slice(s, func(i, j int) bool { return s[i] < s[j] }) }
