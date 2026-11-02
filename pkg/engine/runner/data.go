package runner

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/model"
)

// LoadData reads iteration data from a CSV file (header row = variable
// names) or a JSON array of flat objects.
func LoadData(file string) ([]map[string]string, error) {
	return LoadDataAs(file, model.DataFormat{})
}

// LoadDataAs reads a data file with an explicit format (variable names,
// header line, column mapping, delimiter).
func LoadDataAs(file string, f model.DataFormat) ([]map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	t, err := ParseData(data, filepath.Ext(file), f)
	if err != nil {
		return nil, err
	}
	return t.Rows, nil
}

// Table is a parsed data file: the file's own column names (or positions),
// the variable each column becomes ("" = left out), and the rows keyed by
// variable.
type Table struct {
	Columns   []string            `json:"columns"`
	Variables []string            `json:"variables"`
	Rows      []map[string]string `json:"rows"`
}

// Delimiter returns the field separator a format names (default comma).
func Delimiter(s string) (rune, error) {
	switch s {
	case "":
		return ',', nil
	case "\\t", "\t", "tab":
		return '\t', nil
	}
	if r := []rune(s); len(r) == 1 && r[0] != '"' && r[0] != '\r' && r[0] != '\n' {
		return r[0], nil
	}
	return 0, fmt.Errorf("delimiter %q must be one character (or \"tab\")", s)
}

// ParseData parses CSV (or JSON, by extension or content) with a format.
func ParseData(data []byte, ext string, f model.DataFormat) (*Table, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if strings.EqualFold(ext, ".json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		rows, err := parseJSON(data)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var cols []string
		for _, r := range rows {
			for k := range r {
				if !seen[k] {
					seen[k] = true
					cols = append(cols, k)
				}
			}
		}
		sort.Strings(cols)
		return mapTable(cols, rowsAsRecords(rows, cols), f), nil
	}
	comma, err := Delimiter(f.Delimiter)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = comma
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV data: %w", err)
	}
	header := len(f.Variables) == 0
	if f.Header != nil {
		header = *f.Header
	}
	var cols []string
	switch {
	case len(f.Variables) > 0:
		for _, v := range f.Variables {
			cols = append(cols, strings.TrimSpace(v))
		}
	case header && len(records) > 0:
		for _, h := range records[0] {
			cols = append(cols, strings.TrimSpace(h))
		}
	default:
		return nil, fmt.Errorf("CSV data without a header line needs variable names for its columns")
	}
	if header && len(records) > 0 {
		records = records[1:]
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("CSV data has no data rows")
	}
	return mapTable(cols, records, f), nil
}

func rowsAsRecords(rows []map[string]string, cols []string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		rec := make([]string, len(cols))
		for j, c := range cols {
			rec[j] = r[c]
		}
		out[i] = rec
	}
	return out
}

// mapTable applies Map (column → variable) and builds the rows.
func mapTable(cols []string, records [][]string, f model.DataFormat) *Table {
	t := &Table{Columns: cols, Variables: make([]string, len(cols))}
	for i, c := range cols {
		v := c
		if m, ok := f.Map[c]; ok {
			v = strings.TrimSpace(m)
		}
		t.Variables[i] = v
	}
	t.Rows = make([]map[string]string, 0, len(records))
	for _, rec := range records {
		row := map[string]string{}
		for i, v := range t.Variables {
			if v != "" && i < len(rec) {
				row[v] = rec[i]
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func parseJSON(data []byte) ([]map[string]string, error) {
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("JSON data must be an array of objects: %w", err)
	}
	rows := make([]map[string]string, 0, len(raw))
	for _, obj := range raw {
		row := map[string]string{}
		for k, v := range obj {
			switch t := v.(type) {
			case string:
				row[k] = t
			case nil:
				row[k] = ""
			default:
				b, _ := json.Marshal(t)
				row[k] = string(b)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
