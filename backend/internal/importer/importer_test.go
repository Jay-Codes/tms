package importer_test

import (
	"strings"
	"testing"
	"time"

	"tms/backend/internal/importer"
)

// The file layer's rules, without a database: what the parser tolerates, what
// it refuses, and where each cell error lands.

func TestSniffDelimiter(t *testing.T) {
	cases := map[string]rune{
		"property,unit,rent_amount": ',',
		"property;unit;rent_amount": ';',
		`"a;b",unit,rent_amount`:    ',',
		`"a,b";unit;rent_amount`:    ';',
		"property":                  ',',
		"property;unit\nA;B":        ';',
	}
	for header, want := range cases {
		if got := importer.SniffDelimiter([]byte(header)); got != want {
			t.Errorf("SniffDelimiter(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestParseTakesBOMSemicolonsAndShoutyHeaders(t *testing.T) {
	file := "\ufeff Property ;UNIT;Rent_Amount\n" +
		"Block A;A1;150000\n" +
		"\n" + // a blank line Excel left behind
		"Block A;A2;200000\n"

	rows, err := importer.Parse(importer.KindUnits, []byte(file))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(rows), rows)
	}
	if rows[0].Line != 2 || rows[1].Line != 4 {
		t.Errorf("line numbers = %d, %d — they must count the file, blank lines and all",
			rows[0].Line, rows[1].Line)
	}
	if rows[0].Raw["property"] != "Block A" || rows[0].Raw["unit"] != "A1" {
		t.Errorf("row 1 cells = %v", rows[0].Raw)
	}
}

func TestParseRefusesAHeaderThatDoesNotMatch(t *testing.T) {
	_, err := importer.Parse(importer.KindUnits, []byte("property,unit,rent,extra\nA,B,1,2\n"))
	var he *importer.HeaderError
	if !asHeaderError(err, &he) {
		t.Fatalf("error = %v, want a *HeaderError", err)
	}
	if len(he.Missing) != 1 || he.Missing[0] != "rent_amount" {
		t.Errorf("missing = %v, want [rent_amount]", he.Missing)
	}
	if len(he.Unknown) != 2 {
		t.Errorf("unknown = %v, want both stray columns", he.Unknown)
	}
}

func asHeaderError(err error, target **importer.HeaderError) bool {
	he, ok := err.(*importer.HeaderError)
	if ok {
		*target = he
	}
	return ok
}

func TestParseRefusalsAreCoded(t *testing.T) {
	cases := map[string]struct {
		body string
		code string
	}{
		"empty":       {"", importer.CodeEmptyFile},
		"header only": {"property,unit,rent_amount\n", importer.CodeEmptyFile},
		"not utf-8":   {"property,unit,rent_amount\n\xff\xfe,B,1\n", importer.CodeNotUTF8},
	}
	for name, tc := range cases {
		_, err := importer.Parse(importer.KindUnits, []byte(tc.body))
		fe, ok := err.(*importer.FileError)
		if !ok {
			t.Errorf("%s: error = %v, want a *FileError", name, err)
			continue
		}
		if fe.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", name, fe.Code, tc.code)
		}
	}

	var b strings.Builder
	b.WriteString("property,unit,rent_amount\n")
	for i := 0; i <= importer.MaxRows; i++ {
		b.WriteString("Block A,A,1000\n")
	}
	_, err := importer.Parse(importer.KindUnits, []byte(b.String()))
	fe, ok := err.(*importer.FileError)
	if !ok || fe.Code != importer.CodeTooManyRows {
		t.Errorf("5 001 rows: error = %v, want %s", err, importer.CodeTooManyRows)
	}
}

func TestParseFlagsAShortLineWithoutShiftingItsCells(t *testing.T) {
	rows, err := importer.Parse(importer.KindUnits,
		[]byte("property,unit,rent_amount\nBlock A,A1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Errors[importer.RowErrorKey] == "" {
		t.Errorf("a short line carries no row error: %+v", rows[0])
	}
}

func TestParseKeepsFormulaCellsVerbatim(t *testing.T) {
	rows, err := importer.Parse(importer.KindRenters,
		[]byte("full_name,phone\n\"=cmd|'/c calc'!A1\",0712000001\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := rows[0].Raw["full_name"]; got != `=cmd|'/c calc'!A1` {
		t.Errorf("stored name = %q; cells are stored as typed and neutralised on export", got)
	}
}

// ------------------------------------------------------------ the cells --

func TestParseUnitRow(t *testing.T) {
	row, errs := importer.ParseUnitRow(map[string]string{
		"property": "Block A", "unit": "A1", "rent_amount": "TZS 150,000.00",
	}, 0)
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if row.RentAmount != 150000 {
		t.Errorf("rent_amount = %d, want 150000", row.RentAmount)
	}
	if row.RentPeriodDays != importer.DefaultRentDays {
		t.Errorf("rent_period_days = %d, want the 30-day default", row.RentPeriodDays)
	}
	if row.Status != importer.StatusVacant {
		t.Errorf("status = %q, want vacant", row.Status)
	}

	_, errs = importer.ParseUnitRow(map[string]string{
		"property": "", "unit": "A1", "rent_amount": "0",
		"rent_period_days": "0", "status": "occupied",
	}, 30)
	for _, column := range []string{"property", "rent_amount", "rent_period_days", "status"} {
		if errs[column] == "" {
			t.Errorf("no error on %s: %v", column, errs)
		}
	}
}

func TestParseRenterRowNormalisesThePhone(t *testing.T) {
	row, errs := importer.ParseRenterRow(map[string]string{
		"full_name": "Asha Mollel", "phone": "0712 000 001", "locale": "EN",
	})
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if row.Phone != "+255712000001" {
		t.Errorf("phone = %q, want +255712000001", row.Phone)
	}
	if row.Locale != "en" {
		t.Errorf("locale = %q, want en", row.Locale)
	}

	_, errs = importer.ParseRenterRow(map[string]string{
		"full_name": "", "phone": "12345", "locale": "fr",
	})
	for _, column := range []string{"full_name", "phone", "locale"} {
		if errs[column] == "" {
			t.Errorf("no error on %s: %v", column, errs)
		}
	}
}

func TestParsePaymentRowDatesAndMethods(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, in := range []string{"2026-01-05", "2026-01-05 09:30", "2026-01-05T09:30:00Z"} {
		row, errs := importer.ParsePaymentRow(map[string]string{
			"unit": "A1", "renter_phone": "0712000001", "amount": "150000",
			"paid_at": in, "method": "CASH",
		}, now)
		if len(errs) != 0 {
			t.Fatalf("%s: errors = %v", in, errs)
		}
		if row.PaidAt.Year() != 2026 || row.PaidAt.Month() != time.January {
			t.Errorf("%s parsed as %s", in, row.PaidAt)
		}
		if row.Method != importer.MethodCash {
			t.Errorf("%s: method = %q", in, row.Method)
		}
	}

	_, errs := importer.ParsePaymentRow(map[string]string{
		"unit": "A1", "renter_phone": "0712000001", "amount": "150000",
		"paid_at": "2026-06-01", "method": "gateway",
	}, now)
	if errs["paid_at"] == "" {
		t.Errorf("a date three months ahead is accepted: %v", errs)
	}
	if errs["method"] == "" {
		t.Errorf("`gateway` is not a manual method: %v", errs)
	}
}

func TestTemplateCSVCarriesTheMachineHeaders(t *testing.T) {
	for _, kind := range importer.Kinds() {
		body := string(importer.TemplateCSV(kind))
		lines := strings.Split(strings.TrimSpace(body), "\n")
		if len(lines) != 2 {
			t.Fatalf("%s template has %d lines, want a header and one example", kind, len(lines))
		}
		cols := importer.Columns(kind)
		for _, c := range cols {
			if !strings.Contains(lines[0], c.Name) {
				t.Errorf("%s header does not carry %q: %q", kind, c.Name, lines[0])
			}
		}
		// The template must parse as its own kind.
		if _, err := importer.Parse(kind, []byte(body)); err != nil {
			t.Errorf("%s template does not parse: %v", kind, err)
		}
	}
	if importer.TemplateCSV("nonsense") != nil {
		t.Error("an unknown kind must have no template")
	}
}
