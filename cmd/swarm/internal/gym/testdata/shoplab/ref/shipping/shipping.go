package shipping

import (
	"fmt"
	"strings"

	"shoplab/errs"
	"shoplab/money"
)

type Zone string

const (
	Domestic      Zone = "DOM"
	International Zone = "INTL"
)

const (
	maxWeightG = 30000
	freeOver   = money.Money(5000)
)

func ParseZone(s string) (Zone, error) {
	switch z := Zone(strings.ToUpper(s)); z {
	case Domestic, International:
		return z, nil
	}
	return "", fmt.Errorf("shipping: zone %q: %w", s, errs.ErrInvalid)
}

func Rate(z Zone, weightG int, subtotal money.Money) (money.Money, error) {
	var base, step money.Money
	switch z {
	case Domestic:
		base, step = 500, 150
	case International:
		base, step = 1500, 400
	default:
		return 0, fmt.Errorf("shipping: zone %q: %w", z, errs.ErrInvalid)
	}
	if weightG < 0 || weightG > maxWeightG || subtotal < 0 {
		return 0, fmt.Errorf("shipping: weight %d subtotal %s: %w", weightG, subtotal, errs.ErrInvalid)
	}
	if weightG == 0 || z == Domestic && subtotal >= freeOver {
		return 0, nil
	}
	steps := 0
	if weightG > 1000 {
		steps = (weightG - 1000 + 499) / 500
	}
	return base + step.Mul(steps), nil
}
