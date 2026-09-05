package validate_test

import (
	"errors"
	"testing"

	"tms/backend/internal/validate"
)

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"leading zero", "0712345678", "+255712345678"},
		{"country code no plus", "255712345678", "+255712345678"},
		{"e164", "+255712345678", "+255712345678"},
		{"nine digits", "712345678", "+255712345678"},
		{"spaces", "+255 712 345 678", "+255712345678"},
		{"dashes", "0712-345-678", "+255712345678"},
		{"parens and dots", "(0712).345.678", "+255712345678"},
		{"vodacom 6 prefix", "0678901234", "+255678901234"},
		{"surrounding whitespace", "  0712345678  ", "+255712345678"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := validate.NormalizePhone(tc.in)
			if err != nil {
				t.Fatalf("NormalizePhone(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizePhoneRejects(t *testing.T) {
	t.Parallel()
	bad := []string{
		"",                // empty
		"0812345678",      // 8 is not a mobile prefix
		"071234567",       // too short
		"07123456789",     // too long
		"+254712345678",   // Kenya
		"+255812345678",   // wrong national prefix
		"07123456ab",      // letters
		"+255 712 345 67", // too short after cleanup
		"255712345",       // truncated
	}
	for _, in := range bad {
		if got, err := validate.NormalizePhone(in); err == nil {
			t.Errorf("NormalizePhone(%q) = %q, want error", in, got)
		} else if !errors.Is(err, validate.ErrInvalidPhone) {
			t.Errorf("NormalizePhone(%q) error = %v, want ErrInvalidPhone", in, err)
		}
	}
}

func TestFieldsPhoneRecordsError(t *testing.T) {
	t.Parallel()
	f := validate.Fields{}
	if got := f.Phone("phone", "nonsense"); got != "" {
		t.Fatalf("Phone returned %q for invalid input", got)
	}
	if f.Empty() {
		t.Fatal("expected a field error for an invalid phone")
	}
}

func TestSlugify(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"JJnE Rentals":            "jjne-rentals",
		"  Mbezi  Beach  Block A": "mbezi-beach-block-a",
		"Nyumba & Co.":            "nyumba-co",
		"---":                     "org",
		"":                        "org",
	}
	for in, want := range cases {
		if got := validate.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPINAndPassword(t *testing.T) {
	t.Parallel()
	f := validate.Fields{}
	f.PIN("pin", "123")
	f.Password("password", "short")
	if len(f) != 2 {
		t.Fatalf("expected 2 field errors, got %d: %v", len(f), f)
	}

	ok := validate.Fields{}
	ok.PIN("pin", "1234")
	ok.Password("password", "longenough")
	if !ok.Empty() {
		t.Fatalf("expected no field errors, got %v", ok)
	}
}

// TestIsDigitsASCIIOnly is the L5 guard: non-ASCII digit forms (Arabic-Indic,
// fullwidth) must not pass as OTP/PIN input.
func TestIsDigitsASCIIOnly(t *testing.T) {
	for _, s := range []string{"0", "123456", "0000"} {
		if !validate.IsDigits(s) {
			t.Errorf("IsDigits(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "12a456", "١٢٣٤٥٦", "１２３４５６", "12 34", "+123456"} {
		if validate.IsDigits(s) {
			t.Errorf("IsDigits(%q) = true, want false", s)
		}
	}

	f := validate.Fields{}
	f.PIN("pin", "١٢٣٤")
	if f.Empty() {
		t.Error("PIN() accepted Arabic-Indic digits")
	}
}
