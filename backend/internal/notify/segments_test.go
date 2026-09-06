package notify_test

import (
	"strings"
	"testing"

	"tms/backend/internal/notify"
)

// TestSegments pins the credit unit: one credit per GSM-7 segment of 160
// characters, or per UCS-2 segment of 70 (PLAN2 Phase 14). It is a table
// because the boundaries — 160/161, 70/71, and the drop to 153/67 once a body
// is concatenated — are exactly where a billing bug would hide.
func TestSegments(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		encoding string
		want     int
	}{
		{"empty body still costs one", "", notify.EncodingGSM, 1},
		{"short ascii", "Hello", notify.EncodingGSM, 1},
		{"exactly one gsm segment", strings.Repeat("a", 160), notify.EncodingGSM, 1},
		{"one character over spills to two", strings.Repeat("a", 161), notify.EncodingGSM, 2},
		{"two concatenated segments hold 306", strings.Repeat("a", 306), notify.EncodingGSM, 2},
		{"307 needs a third", strings.Repeat("a", 307), notify.EncodingGSM, 3},

		// Swahili is written in the Latin alphabet with no diacritics, which
		// is the whole reason the platform's default language is not
		// automatically the expensive one: a Swahili reminder costs the same
		// as its English twin.
		{
			name: "swahili reminder is gsm",
			body: "Habari Asha, kodi yako ya TZS 250,000 kwa A-12 katika JJnE " +
				"inatakiwa kulipwa leo, tarehe 2026-10-01.",
			encoding: notify.EncodingGSM, want: 1,
		},
		{
			name:     "swahili at 160 characters is still one segment",
			body:     strings.Repeat("ku", 80),
			encoding: notify.EncodingGSM, want: 1,
		},

		// The GSM extension table costs two septets a character, so a body of
		// braces is half the length it looks.
		{"extension characters cost two septets", strings.Repeat("{", 80), notify.EncodingGSM, 1},
		{"81 extension characters spill", strings.Repeat("{", 81), notify.EncodingGSM, 2},

		// Anything outside the alphabet forces UCS-2 for the whole message.
		{"an emoji forces ucs2", "Karibu \U0001F600", notify.EncodingUCS2, 1},
		{"a diacritic outside gsm forces ucs2", "Tumepokea malipo yakō", notify.EncodingUCS2, 1},
		{"exactly one ucs2 segment", strings.Repeat("ō", 70), notify.EncodingUCS2, 1},
		{"one ucs2 character over spills", strings.Repeat("ō", 71), notify.EncodingUCS2, 2},
		{"two ucs2 segments hold 134", strings.Repeat("ō", 134), notify.EncodingUCS2, 2},
		{"135 ucs2 characters need a third", strings.Repeat("ō", 135), notify.EncodingUCS2, 3},
		// An emoji outside the BMP is a surrogate pair: two UTF-16 units, not
		// one, so 35 of them fill a segment.
		{"astral emoji count as two units", strings.Repeat("\U0001F600", 35), notify.EncodingUCS2, 1},
		{"36 astral emoji spill", strings.Repeat("\U0001F600", 36), notify.EncodingUCS2, 2},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := notify.Encoding(c.body); got != c.encoding {
				t.Errorf("Encoding = %q, want %q", got, c.encoding)
			}
			if got := notify.Segments(c.body); got != c.want {
				t.Errorf("Segments = %d, want %d", got, c.want)
			}
		})
	}
}

// TestSegmentsGSMAlphabetIsNotJustASCII guards the half of the alphabet that a
// naive "is it ASCII?" check would push into UCS-2 and double the price of.
func TestSegmentsGSMAlphabetIsNotJustASCII(t *testing.T) {
	for _, r := range []rune{'£', '¥', 'è', 'é', 'ù', 'ì', 'ò', 'Ç', 'Ø', 'Å', 'Æ', 'ß', 'É', 'ä', 'ö', 'ñ', 'ü', 'à', '§', '¿', '¡'} {
		if got := notify.Encoding(string(r)); got != notify.EncodingGSM {
			t.Errorf("Encoding(%q) = %q, want gsm — it is in the GSM 03.38 alphabet", r, got)
		}
	}
}

// TestParseExemptKinds pins SMS_CREDIT_EXEMPT_KINDS: empty means the default
// (`otp`), and "none" means nothing is exempt. The two must not collapse into
// each other, or an operator who wants to charge for everything would silently
// keep giving verification codes away.
func TestParseExemptKinds(t *testing.T) {
	if got := notify.ParseExemptKinds(""); !got[notify.KindOTP] || len(got) != 1 {
		t.Errorf("default exempt set = %v, want just otp", got)
	}
	if got := notify.ParseExemptKinds("none"); len(got) != 0 {
		t.Errorf("\"none\" exempt set = %v, want empty", got)
	}
	got := notify.ParseExemptKinds("otp, welcome")
	if !got[notify.KindOTP] || !got[notify.KindWelcome] || len(got) != 2 {
		t.Errorf("exempt set = %v, want otp and welcome", got)
	}
}

// TestVariablesForOTPIsOnlyTheCode pins the one kind whose placeholder set is
// not the org-wide eight: `{{code}}` is the only thing a verification message
// may say, and offering an admin `{{amount}}` there would invite a sentence
// the auth path cannot fill in.
func TestVariablesForOTPIsOnlyTheCode(t *testing.T) {
	got := notify.VariablesFor(notify.KindOTP)
	if len(got) != 1 || got[0] != "code" {
		t.Fatalf("VariablesFor(otp) = %v, want [code]", got)
	}
	if vars := notify.VariablesFor(notify.KindReminderDue); len(vars) < 8 {
		t.Fatalf("VariablesFor(reminder_due) = %v, want the eight org variables", vars)
	}
}
