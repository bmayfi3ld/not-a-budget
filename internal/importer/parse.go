package importer

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CanonicalCSVColumns are the accepted CSV header names (matched
// case-insensitively, with spaces normalized to underscores).
var CanonicalCSVColumns = []string{"txn_date", "post_date", "description", "amount", "category", "txn_type", "memo"}

// RequiredCSVColumns must be present in a CSV header.
var RequiredCSVColumns = []string{"txn_date", "description", "amount"}

// ReadCSV parses canonical-CSV content into rows. The header row maps columns by
// name; extra columns are ignored and missing optional columns are blank.
func ReadCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we index by header
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	col := map[string]int{}
	for i, h := range records[0] {
		key := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(h)), " ", "_")
		col[key] = i
	}
	for _, req := range RequiredCSVColumns {
		if _, ok := col[req]; !ok {
			return nil, fmt.Errorf("CSV missing required column %q (need %s)", req, strings.Join(RequiredCSVColumns, ", "))
		}
	}
	get := func(rec []string, name string) string {
		if idx, ok := col[name]; ok && idx < len(rec) {
			return strings.TrimSpace(rec[idx])
		}
		return ""
	}

	var rows []Row
	for i, rec := range records[1:] {
		amtStr := get(rec, "amount")
		if get(rec, "txn_date") == "" && get(rec, "description") == "" && amtStr == "" {
			continue // skip blank lines
		}
		amt, err := ParseAmount(amtStr)
		if err != nil {
			return nil, fmt.Errorf("row %d: bad amount %q: %w", i+2, amtStr, err)
		}
		rows = append(rows, Row{
			TxnDate:     get(rec, "txn_date"),
			PostDate:    get(rec, "post_date"),
			Description: get(rec, "description"),
			Amount:      amt,
			Category:    get(rec, "category"),
			TxnType:     get(rec, "txn_type"),
			Memo:        get(rec, "memo"),
		})
	}
	return rows, nil
}

// ReadJSON parses canonical-JSON content: either a bare array of row objects or
// {"rows":[...],"source":"..."}. The returned source is the wrapper's source
// ("" when absent).
func ReadJSON(r io.Reader) ([]Row, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var rows []Row
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, "", fmt.Errorf("parse JSON array: %w", err)
		}
		return rows, "", nil
	}
	var wrapper struct {
		Rows   []Row  `json:"rows"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, "", fmt.Errorf("parse JSON object: %w", err)
	}
	return wrapper.Rows, wrapper.Source, nil
}

// ParseAmount tolerates currency symbols, thousands separators, and
// parenthesized negatives like "(1,234.56)".
func ParseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = s[1 : len(s)-1]
	}
	s = strings.NewReplacer("$", "", ",", "", " ", "").Replace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if neg {
		v = -v
	}
	return v, nil
}
