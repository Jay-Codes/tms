package httpserver

import (
	"strings"
	"testing"
)

// TestProrateRounding pins the arithmetic of SPEC §4 independently of the
// database: amount × days / period_days, rounded to a whole shilling.
func TestProrateRounding(t *testing.T) {
	cases := []struct {
		name   string
		amount int64
		days   int32
		basis  int32
		want   int64
	}{
		{"7 days of a 30-day price rounds down", 250_000, 7, 30, 58_333},
		{"21 days of a 30-day price is exact", 250_000, 21, 30, 175_000},
		{"45 days of a 30-day price is exact", 250_000, 45, 30, 375_000},
		{"90 days of a 30-day price is exact", 250_000, 90, 30, 750_000},
		{"365 days of a 30-day price rounds up", 250_000, 365, 30, 3_041_667},
		{"the basis period returns the price itself", 250_000, 30, 30, 250_000},
		{"a half shilling rounds away from zero", 1, 1, 2, 1},
		{"a third of a shilling rounds down", 1, 1, 3, 0},
		{"a 7-day basis prorates to 30 days", 70_000, 30, 7, 300_000},
		{"a zero price stays zero", 0, 45, 30, 0},
		{"a zero basis is not divided by", 250_000, 30, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prorate(tc.amount, tc.days, tc.basis); got != tc.want {
				t.Errorf("prorate(%d, %d, %d) = %d, want %d", tc.amount, tc.days, tc.basis, got, tc.want)
			}
		})
	}
}

// TestRandomUnitCode checks the shape of the printed sticker code: ten
// characters, Crockford base32, and not repeating between draws.
func TestRandomUnitCode(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		code, err := randomUnitCode()
		if err != nil {
			t.Fatalf("randomUnitCode: %v", err)
		}
		if len(code) != unitCodeLen {
			t.Fatalf("code %q is %d characters, want %d", code, len(code), unitCodeLen)
		}
		for _, r := range code {
			if !strings.ContainsRune(crockfordAlphabet, r) {
				t.Fatalf("code %q contains %q, which is not in the Crockford alphabet", code, r)
			}
		}
		if seen[code] {
			t.Fatalf("code %q was drawn twice in 500 draws", code)
		}
		seen[code] = true
	}
}

// TestQRObjectKey documents the bucket layout (SPEC §7).
func TestQRObjectKey(t *testing.T) {
	got := qrObjectKey("org-1", "unit-2")
	if got != "org-1/unit-2.png" {
		t.Errorf("qrObjectKey = %q, want org-1/unit-2.png", got)
	}
}
