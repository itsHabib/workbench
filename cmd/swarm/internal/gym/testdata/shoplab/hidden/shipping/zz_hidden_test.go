package shipping

import (
	"errors"
	"testing"

	"shoplab/errs"
	"shoplab/money"
)

func TestHiddenParseZone(t *testing.T) {
	for in, want := range map[string]Zone{"dom": Domestic, "DOM": Domestic, "Intl": International, "INTL": International} {
		if got, err := ParseZone(in); err != nil || got != want {
			t.Errorf("ParseZone(%q) = %q, %v", in, got, err)
		}
	}
	if Domestic != "DOM" || International != "INTL" {
		t.Fatal("zone values")
	}
	for _, in := range []string{"", "domestic", "EU", " dom"} {
		if _, err := ParseZone(in); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("ParseZone(%q) err = %v", in, err)
		}
	}
}

func TestHiddenDomesticSteps(t *testing.T) {
	for grams, want := range map[int]money.Money{0: 0, 1: 500, 1000: 500, 1001: 650, 1500: 650, 1501: 800, 2000: 800, 30000: 500 + 58*150} {
		got, err := Rate(Domestic, grams, 1000)
		if err != nil || got != want {
			t.Errorf("%d g = %d, %v; want %d", grams, got, err, want)
		}
	}
}

func TestHiddenDomesticFreeOverFifty(t *testing.T) {
	if got, _ := Rate(Domestic, 5000, 4999); got == 0 {
		t.Fatal("free below 50.00")
	}
	for _, sub := range []money.Money{5000, 5001, 999999} {
		if got, err := Rate(Domestic, 5000, sub); err != nil || got != 0 {
			t.Errorf("subtotal %d: %d, %v", sub, got, err)
		}
	}
}

func TestHiddenInternational(t *testing.T) {
	for grams, want := range map[int]money.Money{0: 0, 500: 1500, 1000: 1500, 1001: 1900, 2500: 2700} {
		got, err := Rate(International, grams, 100000)
		if err != nil || got != want {
			t.Errorf("%d g = %d, %v; want %d", grams, got, err, want)
		}
	}
}

func TestHiddenRateRejects(t *testing.T) {
	for name, fn := range map[string]func() (money.Money, error){
		"zone":           func() (money.Money, error) { return Rate("EU", 100, 100) },
		"weight":         func() (money.Money, error) { return Rate(Domestic, -1, 100) },
		"heavy":          func() (money.Money, error) { return Rate(International, 30001, 100) },
		"subtotal":       func() (money.Money, error) { return Rate(Domestic, 100, -1) },
		"heavy but free": func() (money.Money, error) { return Rate(Domestic, 30001, 9000) },
	} {
		if _, err := fn(); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
