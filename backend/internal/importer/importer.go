// Package importer parses and validates the CSV files a landlord uploads to
// bring previous records into TMS (PLAN2 Phase 16 §16.2).
//
// It owns two things and deliberately no more:
//
//   - the file layer — encoding, delimiter, header matching, row limits;
//   - the per-cell rules — what a well-formed `rent_amount`, `phone`,
//     `paid_at` or `method` looks like, mirroring the manual endpoints'
//     validation exactly.
//
// Everything that needs the database (does this property exist? whose contract
// is this unit's?) is resolution, and lives in the HTTP layer beside the
// service functions a commit calls. This package never touches Postgres, so
// the rules can be table-tested without one.
package importer

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tms/backend/internal/validate"
)

// The three sheets a landlord may bring (PLAN2 §16.2).
const (
	KindUnits    = "units"
	KindRenters  = "renters"
	KindPayments = "payments"
)

// File limits (PLAN2 §16.2).
const (
	// MaxBytes is the largest upload accepted, before decoding.
	MaxBytes = 2 << 20 // 2 MiB
	// MaxRows is the largest number of *data* rows accepted; the header does
	// not count towards it.
	MaxRows = 5000
)

// Cell bounds. These mirror the manual endpoints' own limits (unit and property
// names, the money range, the reference and note lengths), so a row refused by
// the importer would have been refused by the form too.
const (
	NameMax          = 120
	FullNameMax      = 120
	ReferenceMax     = 80
	NoteMax          = 500
	AmountMax        = 1_000_000_000_000 // exclusive, as checkAmount
	PeriodDaysMax    = 3650
	DefaultRentDays  = 30
	futureToleranceH = 24
)

// RowErrors maps a column name to the reason that cell was refused. The key
// `_row` carries a whole-line problem (a short or long record).
type RowErrors map[string]string

// RowErrorKey is the key used for a problem that belongs to the line rather
// than to one of its columns.
const RowErrorKey = "_row"

// Add records the first error seen for a column; later ones are noise.
func (e RowErrors) Add(column, msg string) {
	if _, seen := e[column]; !seen {
		e[column] = msg
	}
}

// Column is one machine header of a template. The headers are fixed English
// identifiers in every language (PLAN2 §16.2): they are what the file must
// contain, not what the screen shows.
type Column struct {
	Name     string
	Required bool
	Example  string
	// Help is the one-line description the template page prints.
	Help string
}

//nolint:gochecknoglobals // fixed column tables, read-only.
var columns = map[string][]Column{
	KindUnits: {
		{Name: "property", Required: true, Example: "Block A", Help: "property name; created when it does not exist yet"},
		{Name: "unit", Required: true, Example: "A1", Help: "unit name, unique within the property"},
		{Name: "rent_amount", Required: true, Example: "150000", Help: "whole shillings"},
		{Name: "rent_period_days", Required: false, Example: "30", Help: "days the rent covers (default 30)"},
		{Name: "status", Required: false, Example: "vacant", Help: "vacant or unlisted (default vacant)"},
	},
	KindRenters: {
		{Name: "full_name", Required: true, Example: "Asha Mollel", Help: "the renter's name"},
		{Name: "phone", Required: true, Example: "0712000001", Help: "Tanzanian number; normalised to +255…"},
		{Name: "locale", Required: false, Example: "sw", Help: "sw or en (default sw)"},
		{Name: "property", Required: false, Example: "Block A", Help: "property of the unit below"},
		{Name: "unit", Required: false, Example: "A1", Help: "when given, a contract is drawn up for signing"},
	},
	KindPayments: {
		{Name: "unit", Required: true, Example: "A1", Help: "unit name; must be unique across the org"},
		{Name: "renter_phone", Required: true, Example: "0712000001", Help: "the renter who paid"},
		{Name: "amount", Required: true, Example: "150000", Help: "whole shillings"},
		{Name: "paid_at", Required: true, Example: "2026-01-05", Help: "YYYY-MM-DD, or with a time"},
		{Name: "method", Required: true, Example: "cash", Help: "cash, bank_transfer or mobile_money_manual"},
		{Name: "reference", Required: false, Example: "RCT-001", Help: "receipt or transaction reference"},
		{Name: "note", Required: false, Example: "January rent", Help: "free text"},
	},
}

// Kinds lists the sheets, in the order the picker shows them.
//
//nolint:gochecknoglobals // fixed list, read-only.
var kindOrder = []string{KindUnits, KindRenters, KindPayments}

// Kinds returns the accepted `kind` values.
func Kinds() []string { return append([]string(nil), kindOrder...) }

// Valid reports whether kind names a sheet.
func Valid(kind string) bool { _, ok := columns[kind]; return ok }

// Columns returns the column table for a kind (nil for an unknown one).
func Columns(kind string) []Column { return columns[kind] }

// TemplateCSV renders the downloadable template: the machine header row and one
// example line. Cells are written through encoding/csv, so a value containing
// the delimiter is quoted rather than mangled.
func TemplateCSV(kind string) []byte {
	cols := columns[kind]
	if cols == nil {
		return nil
	}
	head := make([]string, 0, len(cols))
	example := make([]string, 0, len(cols))
	for _, c := range cols {
		head = append(head, c.Name)
		example = append(example, c.Example)
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(head)
	_ = w.Write(example)
	w.Flush()
	return buf.Bytes()
}

// ------------------------------------------------------------- the file --

// Row is one data line of the file: its 1-based line number as the landlord's
// spreadsheet counts it (the header is line 1), and its cells keyed by the
// machine column name. Values are stored verbatim — a cell opening with `=` is
// kept as typed and neutralised on export, never on the way in.
type Row struct {
	Line int
	Raw  map[string]string
	// Errors carries the whole-line problems the parser itself found (a short
	// or long record). Cell rules run later.
	Errors RowErrors
}

// HeaderError is a file whose header row does not match the kind: it names
// every column that is missing and every one nobody recognised, so the landlord
// fixes the sheet once rather than a column at a time.
type HeaderError struct {
	Missing []string
	Unknown []string
}

func (e *HeaderError) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Unknown) > 0 {
		parts = append(parts, "unknown: "+strings.Join(e.Unknown, ", "))
	}
	return "csv header does not match the template (" + strings.Join(parts, "; ") + ")"
}

// FileError is a file that cannot be read at all: the wrong encoding, no rows,
// too many rows, or CSV the parser cannot follow.
type FileError struct {
	Code    string
	Message string
}

func (e *FileError) Error() string { return e.Message }

// File error codes.
const (
	CodeNotUTF8      = "not_utf8"
	CodeEmptyFile    = "empty_file"
	CodeNoHeader     = "no_header"
	CodeTooManyRows  = "too_many_rows"
	CodeUnreadable   = "unreadable_csv"
	CodeUnknownKind  = "unknown_kind"
	CodeFileTooLarge = "file_too_large"
)

var errUnknownKind = &FileError{Code: CodeUnknownKind, Message: "unknown import kind"}

// bom is the UTF-8 byte-order mark Excel writes in front of a CSV.
var bom = []byte{0xEF, 0xBB, 0xBF}

// SniffDelimiter picks the separator from the header line: whichever of `,` and
// `;` occurs more often outside quotes wins, and a tie is a comma. Excel in a
// Swahili or European locale writes semicolons, and a landlord should not have
// to know that.
func SniffDelimiter(data []byte) rune {
	line := data
	if i := bytes.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	var commas, semis int
	inQuote := false
	for _, b := range line {
		switch {
		case b == '"':
			inQuote = !inQuote
		case inQuote:
		case b == ',':
			commas++
		case b == ';':
			semis++
		}
	}
	if semis > commas {
		return ';'
	}
	return ','
}

// Parse reads an uploaded file into rows keyed by the kind's machine columns.
//
// It is tolerant where a spreadsheet is careless — a UTF-8 BOM, a `;`
// delimiter, header capitalisation and surrounding spaces, blank lines — and
// strict where tolerance would hide a mistake: an unknown or missing column is
// a refusal naming both lists, and a row with the wrong number of cells is an
// error on that line rather than a silent shift.
func Parse(kind string, data []byte) ([]Row, error) {
	cols := columns[kind]
	if cols == nil {
		return nil, errUnknownKind
	}
	if len(data) > MaxBytes {
		return nil, &FileError{Code: CodeFileTooLarge, Message: "the file is larger than 2 MiB"}
	}
	data = bytes.TrimPrefix(data, bom)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, &FileError{Code: CodeEmptyFile, Message: "the file is empty"}
	}
	if !utf8.Valid(data) {
		return nil, &FileError{Code: CodeNotUTF8, Message: "the file is not UTF-8 text; save it as CSV UTF-8"}
	}

	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = SniffDelimiter(data)
	r.FieldsPerRecord = -1 // per-row, so one bad line does not sink the file
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, &FileError{Code: CodeNoHeader, Message: "the file has no header row"}
	}
	if err != nil {
		return nil, &FileError{Code: CodeUnreadable, Message: "the file could not be read as CSV: " + err.Error()}
	}

	index, herr := matchHeader(cols, header)
	if herr != nil {
		return nil, herr
	}

	rows := make([]Row, 0, 64)
	lastLine := 1 // the header
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// The line number comes from the parser, so a quoted cell spanning
			// two lines does not shift every later row's number.
			line := lastLine + 1
			var pe *csv.ParseError
			if errors.As(err, &pe) && pe.Line > 0 {
				line = pe.Line
			}
			lastLine = line
			rows = append(rows, Row{
				Line:   line,
				Raw:    map[string]string{},
				Errors: RowErrors{RowErrorKey: "this line could not be read as CSV"},
			})
			if len(rows) > MaxRows {
				return nil, tooManyRows()
			}
			continue
		}
		if blank(rec) {
			continue
		}
		// encoding/csv skips blank lines silently, so the record's own position
		// is the only honest answer to "which line of my spreadsheet is this?".
		line := lastLine + 1
		if len(rec) > 0 {
			if at, _ := r.FieldPos(0); at > 0 {
				line = at
			}
		}
		lastLine = line
		row := Row{Line: line, Raw: make(map[string]string, len(cols))}
		if len(rec) != len(header) {
			row.Errors = RowErrors{RowErrorKey: fmt.Sprintf(
				"this line has %d cells; the header has %d", len(rec), len(header))}
		}
		for _, c := range cols {
			pos, ok := index[c.Name]
			if !ok || pos >= len(rec) {
				row.Raw[c.Name] = ""
				continue
			}
			row.Raw[c.Name] = strings.TrimSpace(rec[pos])
		}
		rows = append(rows, row)
		if len(rows) > MaxRows {
			return nil, tooManyRows()
		}
	}
	if len(rows) == 0 {
		return nil, &FileError{Code: CodeEmptyFile, Message: "the file has a header but no data rows"}
	}
	return rows, nil
}

func tooManyRows() *FileError {
	return &FileError{
		Code:    CodeTooManyRows,
		Message: fmt.Sprintf("the file has more than %d data rows; split it", MaxRows),
	}
}

func blank(rec []string) bool {
	for _, cell := range rec {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// matchHeader maps each known column to its position, case-insensitively and
// ignoring surrounding space. A duplicate header keeps the first occurrence.
func matchHeader(cols []Column, header []string) (map[string]int, *HeaderError) {
	known := make(map[string]bool, len(cols))
	for _, c := range cols {
		known[c.Name] = true
	}
	index := map[string]int{}
	var unknown []string
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, string(bom))))
		switch {
		case !known[name]:
			if name != "" {
				unknown = append(unknown, strings.TrimSpace(h))
			}
		case index[name] == 0 && !hasIndex(index, name):
			index[name] = i
		}
	}
	var missing []string
	for _, c := range cols {
		if !c.Required {
			continue
		}
		if !hasIndex(index, c.Name) {
			missing = append(missing, c.Name)
		}
	}
	if len(missing) > 0 || len(unknown) > 0 {
		return nil, &HeaderError{Missing: missing, Unknown: unknown}
	}
	return index, nil
}

func hasIndex(index map[string]int, name string) bool {
	_, ok := index[name]
	return ok
}

// --------------------------------------------------------- the cell rules --

// Unit statuses an import may set. A unit is never imported `occupied`: what
// makes a unit occupied is an active contract, not a spreadsheet cell.
const (
	StatusVacant   = "vacant"
	StatusUnlisted = "unlisted"
)

// Payment methods an import may carry — the three manual values (PLAN2 §16.2).
const (
	MethodCash              = "cash"
	MethodBankTransfer      = "bank_transfer"
	MethodMobileMoneyManual = "mobile_money_manual"
)

// UnitRow is a validated line of a `units` sheet.
type UnitRow struct {
	Property       string
	Unit           string
	RentAmount     int64
	RentPeriodDays int32
	Status         string
}

// ParseUnitRow applies the `units` rules to one line's cells.
func ParseUnitRow(raw map[string]string, defaultPeriodDays int32) (UnitRow, RowErrors) {
	errs := RowErrors{}
	out := UnitRow{
		Property: text(errs, "property", raw["property"], NameMax, true),
		Unit:     text(errs, "unit", raw["unit"], NameMax, true),
	}
	out.RentAmount = money(errs, "rent_amount", raw["rent_amount"], true)

	if defaultPeriodDays <= 0 || defaultPeriodDays > PeriodDaysMax {
		defaultPeriodDays = DefaultRentDays
	}
	out.RentPeriodDays = defaultPeriodDays
	if v := raw["rent_period_days"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > PeriodDaysMax {
			errs.Add("rent_period_days", "must be a whole number of days between 1 and 3650")
		} else {
			out.RentPeriodDays = int32(n)
		}
	}

	out.Status = StatusVacant
	if v := strings.ToLower(raw["status"]); v != "" {
		if v != StatusVacant && v != StatusUnlisted {
			errs.Add("status", "must be vacant or unlisted")
		} else {
			out.Status = v
		}
	}
	return out, errs
}

// RenterRow is a validated line of a `renters` sheet.
type RenterRow struct {
	FullName string
	Phone    string // E.164, normalised like registration
	Locale   string
	Property string
	Unit     string
}

// ParseRenterRow applies the `renters` rules to one line's cells.
func ParseRenterRow(raw map[string]string) (RenterRow, RowErrors) {
	errs := RowErrors{}
	out := RenterRow{
		FullName: text(errs, "full_name", raw["full_name"], FullNameMax, true),
		Property: text(errs, "property", raw["property"], NameMax, false),
		Unit:     text(errs, "unit", raw["unit"], NameMax, false),
		Locale:   "sw",
	}
	if v := raw["phone"]; v == "" {
		errs.Add("phone", "phone is required")
	} else if phone, err := validate.NormalizePhone(v); err != nil {
		errs.Add("phone", "must be a Tanzanian phone number")
	} else {
		out.Phone = phone
	}
	if v := strings.ToLower(raw["locale"]); v != "" {
		if v != "sw" && v != "en" {
			errs.Add("locale", "must be sw or en")
		} else {
			out.Locale = v
		}
	}
	return out, errs
}

// PaymentRow is a validated line of a `payments` sheet.
type PaymentRow struct {
	Unit        string
	RenterPhone string
	Amount      int64
	PaidAt      time.Time
	Method      string
	Reference   string
	Note        string
}

// paidAtLayouts are the shapes a spreadsheet writes a date in. A bare date is
// read as midnight UTC, which is how the rest of the API stores a day.
//
//nolint:gochecknoglobals // fixed list, read-only.
var paidAtLayouts = []string{
	"2006-01-02",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	time.RFC3339,
}

// ParsePaymentRow applies the `payments` rules to one line's cells. `now` is
// the clock the future-date check runs against.
func ParsePaymentRow(raw map[string]string, now time.Time) (PaymentRow, RowErrors) {
	errs := RowErrors{}
	out := PaymentRow{
		Unit:      text(errs, "unit", raw["unit"], NameMax, true),
		Reference: text(errs, "reference", raw["reference"], ReferenceMax, false),
		Note:      text(errs, "note", raw["note"], NoteMax, false),
		Amount:    money(errs, "amount", raw["amount"], true),
	}
	if v := raw["renter_phone"]; v == "" {
		errs.Add("renter_phone", "renter_phone is required")
	} else if phone, err := validate.NormalizePhone(v); err != nil {
		errs.Add("renter_phone", "must be a Tanzanian phone number")
	} else {
		out.RenterPhone = phone
	}

	switch v := strings.ToLower(raw["method"]); v {
	case "":
		errs.Add("method", "method is required")
	case MethodCash, MethodBankTransfer, MethodMobileMoneyManual:
		out.Method = v
	default:
		errs.Add("method", "must be cash, bank_transfer or mobile_money_manual")
	}

	if v := raw["paid_at"]; v == "" {
		errs.Add("paid_at", "paid_at is required")
	} else if t, ok := parsePaidAt(v); !ok {
		errs.Add("paid_at", "must be a date (YYYY-MM-DD), optionally with a time")
	} else if t.After(now.Add(futureToleranceH * time.Hour)) {
		errs.Add("paid_at", "cannot be more than a day in the future")
	} else {
		out.PaidAt = t
	}
	return out, errs
}

func parsePaidAt(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, layout := range paidAtLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// text trims, bounds and (when required) insists on one free-text cell.
func text(errs RowErrors, name, v string, maxLen int, required bool) string {
	v = strings.TrimSpace(v)
	switch {
	case v == "" && required:
		errs.Add(name, name+" is required")
	case len([]rune(v)) > maxLen:
		errs.Add(name, fmt.Sprintf("must be at most %d characters", maxLen))
	}
	return v
}

// money reads a whole-shilling amount, tolerating the thousands separators and
// currency prefix a spreadsheet leaves behind ("TZS 150,000", "150 000").
func money(errs RowErrors, name, v string, required bool) int64 {
	raw := strings.TrimSpace(v)
	if raw == "" {
		if required {
			errs.Add(name, name+" is required")
		}
		return 0
	}
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ',', ' ', ' ', '\'':
			return -1
		}
		return r
	}, strings.TrimPrefix(strings.ToUpper(raw), "TZS"))
	if dot := strings.IndexByte(cleaned, '.'); dot >= 0 {
		// A trailing ".00" is Excel formatting, not cents: TMS money is whole
		// shillings, so anything else after the point is a refusal.
		if strings.Trim(cleaned[dot+1:], "0") != "" {
			errs.Add(name, "must be a whole number of shillings")
			return 0
		}
		cleaned = cleaned[:dot]
	}
	n, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil || n <= 0 || n >= AmountMax {
		errs.Add(name, "must be a whole number of shillings between 1 and 999,999,999,999")
		return 0
	}
	return n
}
