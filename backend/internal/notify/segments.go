package notify

import "strings"

// SMS segmentation (GSM 03.38 / 3GPP TS 23.038), which is what one credit is
// counted against (PLAN2 Phase 14: "1 credit per 160-character GSM segment,
// 70 for UCS-2").
//
// A message is billed by the provider in segments, not in messages: a body
// that fits the 7-bit alphabet packs 160 characters into one segment, and a
// body carrying anything outside it — an emoji, a diacritic the alphabet does
// not have — is sent as UCS-2 at 70 characters. Longer bodies are concatenated,
// and concatenation costs a 6-octet user-data header out of every segment, so
// the per-segment room drops to 153 (GSM) or 67 (UCS-2).
const (
	// GSMSingle is the longest GSM-7 body that still fits one segment.
	GSMSingle = 160
	// GSMMulti is the room per segment once a GSM-7 body is concatenated.
	GSMMulti = 153
	// UCS2Single is the longest UCS-2 body that still fits one segment.
	UCS2Single = 70
	// UCS2Multi is the room per segment once a UCS-2 body is concatenated.
	UCS2Multi = 67
)

// Encodings a body may be sent in.
const (
	EncodingGSM  = "gsm"
	EncodingUCS2 = "ucs2"
)

// gsmBasic is the GSM 03.38 default alphabet: one septet per character.
const gsmBasic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
	"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"

// gsmExtended is the escape-table half of the alphabet. Each of these costs
// two septets, because it travels as ESC followed by the character.
const gsmExtended = "^{}\\[~]|€"

// gsmSeptets maps a rune to the number of septets it occupies, or 0 when the
// GSM alphabet cannot carry it at all.
//
//nolint:gochecknoglobals // fixed alphabet table, read-only after init.
var gsmSeptets = buildGSMTable()

func buildGSMTable() map[rune]int {
	t := make(map[rune]int, len(gsmBasic)+len(gsmExtended))
	for _, r := range gsmBasic {
		t[r] = 1
	}
	for _, r := range gsmExtended {
		t[r] = 2
	}
	// A tab is not in the alphabet, but a landlord's two-line notice may carry
	// one and turning the whole message into UCS-2 over it would double its
	// price. It travels as the extended-table form feed's neighbour in every
	// gateway that accepts it; count it as an escaped character.
	t['\t'] = 2
	return t
}

// Encoding reports the alphabet a body has to be sent in: "gsm" when every
// character fits GSM 03.38, "ucs2" otherwise.
func Encoding(body string) string {
	for _, r := range body {
		if gsmSeptets[r] == 0 {
			return EncodingUCS2
		}
	}
	return EncodingGSM
}

// Segments returns how many SMS segments a body occupies — the number of
// credits one send of it costs.
//
// An empty body is one segment: the provider is still handed a message, and
// billing a zero would let a whitespace template send for free.
func Segments(body string) int {
	if Encoding(body) == EncodingUCS2 {
		// UCS-2 counts UTF-16 code units, so an emoji outside the BMP is two.
		n := utf16Units(body)
		return segmentsFor(n, UCS2Single, UCS2Multi)
	}
	n := 0
	for _, r := range body {
		n += gsmSeptets[r]
	}
	return segmentsFor(n, GSMSingle, GSMMulti)
}

// segmentsFor divides a length into segments, allowing for the header a
// concatenated message pays in every one of its parts.
func segmentsFor(n, single, multi int) int {
	if n <= single {
		return 1
	}
	return (n + multi - 1) / multi
}

// utf16Units counts the UTF-16 code units in s — the unit UCS-2 payloads are
// measured in, so a surrogate pair (an emoji) counts as two.
func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// SegmentsPair reports the segment count of both languages of one template,
// which is what the admin editor shows beside each box.
func SegmentsPair(sw, en string) map[string]int {
	return map[string]int{LangSwahili: Segments(sw), LangEnglish: Segments(en)}
}

// ExemptKinds is the default set of notification kinds that send without
// debiting a credit: the platform absorbs the cost of letting somebody into
// their own account (PLAN2 Phase 14, confirmed with the client). It is
// overridable with SMS_CREDIT_EXEMPT_KINDS.
//
//nolint:gochecknoglobals // fixed default, read-only.
var ExemptKinds = []string{KindOTP}

// ParseExemptKinds reads the SMS_CREDIT_EXEMPT_KINDS setting: a
// comma-separated kind list. An empty setting means the default (`otp`); the
// literal "none" means nothing is exempt, so an operator can say "charge for
// everything" without the empty string meaning two different things.
func ParseExemptKinds(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	out := map[string]bool{}
	if raw == "" {
		for _, k := range ExemptKinds {
			out[k] = true
		}
		return out
	}
	if strings.EqualFold(raw, "none") {
		return out
	}
	for _, part := range strings.Split(raw, ",") {
		if k := strings.TrimSpace(part); k != "" {
			out[k] = true
		}
	}
	return out
}
