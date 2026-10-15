package bulkimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"shopee/backend/pkg/apperror"
)

var (
	utf8BOM       = []byte{0xEF, 0xBB, 0xBF}
	dynamicCode   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	markupPattern = regexp.MustCompile(`<[A-Za-z/!?]`)
	linkPattern   = regexp.MustCompile(`(?i)://|(^|[^a-z])(javascript|data|vbscript):|(^|[^a-z0-9])www\.`)
)

// MaxCellBytes bounds one cell (a long product description fits).
const MaxCellBytes = 60000

// FileInvalid is the 422 returned for a file the parser refuses as a whole.
func FileInvalid(message string) *apperror.Error {
	return Problem(http.StatusUnprocessableEntity, "file_invalid", message)
}

// Parsed is a structurally valid file.
type Parsed struct {
	Header  []string
	Records []Record
	// Raw holds each record's cells in header order (for the error report).
	Raw map[int][]string
}

// Parse reads a UTF-8 CSV against a template. It enforces the byte and row
// limits while reading and never loads more than maxBytes+1 bytes.
func Parse(r io.Reader, t Template, maxRows int, maxBytes int64) (*Parsed, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, FileInvalid(fmt.Sprintf("File is larger than %d bytes", maxBytes))
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, FileInvalid("File must be UTF-8 CSV text")
	}
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = 0
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, FileInvalid("File is empty")
	}
	if err != nil {
		return nil, FileInvalid("Malformed CSV header")
	}
	header, err = checkHeader(header, t)
	if err != nil {
		return nil, err
	}
	out := &Parsed{Header: header, Raw: map[int][]string{}}
	for rowNo := 1; ; rowNo++ {
		cells, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				return nil, FileInvalid(fmt.Sprintf("Malformed CSV at line %d: every row needs %d columns and balanced quotes", pe.Line, len(header)))
			}
			return nil, FileInvalid("Malformed CSV")
		}
		empty := true
		for _, c := range cells {
			if len(c) > MaxCellBytes {
				return nil, FileInvalid(fmt.Sprintf("Row %d has a cell longer than %d bytes", rowNo, MaxCellBytes))
			}
			empty = empty && strings.TrimSpace(c) == ""
		}
		if empty {
			continue
		}
		if len(out.Records) == maxRows {
			return nil, FileInvalid(fmt.Sprintf("File has more than %d data rows; split it into several jobs", maxRows))
		}
		values := make(map[string]string, len(header))
		for i, name := range header {
			values[name] = strings.TrimSpace(cells[i])
		}
		out.Records = append(out.Records, Record{RowNo: rowNo, Values: values})
		out.Raw[rowNo] = cells
	}
	if len(out.Records) == 0 {
		return nil, FileInvalid("File has no data rows")
	}
	return out, nil
}

func checkHeader(raw []string, t Template) ([]string, error) {
	known := map[string]Column{}
	for _, c := range t.Columns {
		known[c.Name] = c
	}
	seen := map[string]bool{}
	header := make([]string, len(raw))
	for i, h := range raw {
		name := strings.ToLower(strings.TrimSpace(h))
		if name == "" {
			return nil, FileInvalid(fmt.Sprintf("Header column %d is empty", i+1))
		}
		if seen[name] {
			return nil, FileInvalid(fmt.Sprintf("Header column %q is repeated", name))
		}
		seen[name] = true
		if _, ok := known[name]; !ok {
			code, dynamic := strings.CutPrefix(name, t.DynamicPrefix)
			if t.DynamicPrefix == "" || !dynamic || !dynamicCode.MatchString(code) {
				return nil, FileInvalid(fmt.Sprintf("Unknown column %q for template %s v%d", name, t.Kind, t.Version))
			}
		}
		header[i] = name
	}
	for _, c := range t.Columns {
		if c.Required && !seen[c.Name] {
			return nil, FileInvalid(fmt.Sprintf("Required column %q is missing", c.Name))
		}
	}
	return header, nil
}

// CheckCells applies the rules every imported text cell follows: no
// spreadsheet formulas, no markup, no links or embedded media.
func CheckCells(rec Record, t Template) *RowError {
	readOnly := map[string]bool{}
	for _, c := range t.Columns {
		readOnly[c.Name] = c.ReadOnly
	}
	for name, v := range rec.Values {
		if v == "" || readOnly[name] {
			continue
		}
		if e := checkCell(name, v); e != nil {
			return e
		}
	}
	return nil
}

func checkCell(name, v string) *RowError {
	if strings.ContainsAny(v[:1], "=+-@") {
		return &RowError{Code: "unsafe_value", Field: name, Message: "Values must not start with =, +, - or @ (spreadsheet formulas are not accepted)"}
	}
	for _, r := range v {
		if unicode.IsControl(r) && r != '\n' && r != '\r' {
			return &RowError{Code: "unsafe_value", Field: name, Message: "Values must not contain control characters"}
		}
	}
	if markupPattern.MatchString(v) {
		return &RowError{Code: "unsafe_value", Field: name, Message: "HTML is not accepted; use plain text"}
	}
	if linkPattern.MatchString(v) {
		return &RowError{Code: "unsafe_value", Field: name, Message: "Links and media URLs are not accepted; add media in the product editor"}
	}
	return nil
}

// Neutralize keeps a text cell from being evaluated as a formula when the
// CSV is opened in a spreadsheet.
func Neutralize(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
		return "'" + v
	}
	return v
}

// CSVWriter writes a UTF-8 (with BOM, for spreadsheets) CSV whose every
// cell is neutralized.
type CSVWriter struct{ w *csv.Writer }

func NewCSVWriter(w io.Writer) (*CSVWriter, error) {
	if _, err := w.Write(utf8BOM); err != nil {
		return nil, err
	}
	return &CSVWriter{w: csv.NewWriter(w)}, nil
}

func (c *CSVWriter) Write(cells []string) error {
	out := make([]string, len(cells))
	for i, v := range cells {
		out[i] = Neutralize(v)
	}
	return c.w.Write(out)
}

func (c *CSVWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}
