package runner

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadData reads iteration data from a CSV file (header row = variable
// names) or a JSON array of flat objects.
func LoadData(file string) ([]map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(file), ".json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		return parseJSON(data)
	}
	return parseCSV(data)
}

func parseCSV(data []byte) ([]map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV data: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV data needs a header row and at least one data row")
	}
	head := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := map[string]string{}
		for i, h := range head {
			if i < len(rec) {
				row[strings.TrimSpace(h)] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
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
