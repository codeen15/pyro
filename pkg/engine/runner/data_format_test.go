package runner

import (
	"reflect"
	"strings"
	"testing"

	"github.com/codeen15/pyro/pkg/engine/model"
)

func TestParseDataFormats(t *testing.T) {
	yes, no := true, false
	csvHeader := "user,pass,region\nada,s3cret,eu\nbob,hunter2,us\n"
	cases := []struct {
		name string
		data string
		f    model.DataFormat
		vars []string
		row0 map[string]string
		n    int
	}{
		{"header line names the columns", csvHeader, model.DataFormat{}, []string{"user", "pass", "region"},
			map[string]string{"user": "ada", "pass": "s3cret", "region": "eu"}, 2},
		{"map renames and drops columns", csvHeader, model.DataFormat{Map: map[string]string{"user": "username", "region": ""}}, []string{"username", "pass", ""},
			map[string]string{"username": "ada", "pass": "s3cret"}, 2},
		{"variable names for a file without a header (JMeter)", "ada;s3cret\nbob;hunter2\n", model.DataFormat{Variables: []string{"user", " pass "}, Delimiter: ";"}, []string{"user", "pass"},
			map[string]string{"user": "ada", "pass": "s3cret"}, 2},
		{"variable names and a header line to skip", csvHeader, model.DataFormat{Variables: []string{"u", "p"}, Header: &yes}, []string{"u", "p"},
			map[string]string{"u": "ada", "p": "s3cret"}, 2},
		{"tabs", "a\tb\n1\t2\n", model.DataFormat{Delimiter: "tab"}, []string{"a", "b"}, map[string]string{"a": "1", "b": "2"}, 1},
		{"json with map", `[{"id":1,"name":"x"},{"id":2,"name":"y"}]`, model.DataFormat{Map: map[string]string{"id": "userId"}}, []string{"userId", "name"},
			map[string]string{"userId": "1", "name": "x"}, 2},
	}
	for _, c := range cases {
		ext := ".csv"
		if strings.HasPrefix(c.data, "[") {
			ext = ".json"
		}
		tb, err := ParseData([]byte(c.data), ext, c.f)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(tb.Variables, c.vars) || !reflect.DeepEqual(tb.Rows[0], c.row0) || len(tb.Rows) != c.n {
			t.Errorf("%s: vars=%v row0=%v n=%d", c.name, tb.Variables, tb.Rows[0], len(tb.Rows))
		}
	}
	if _, err := ParseData([]byte("a,b\n1,2\n"), ".csv", model.DataFormat{Header: &no}); err == nil {
		t.Error("no header and no variable names should be refused")
	}
	if _, err := ParseData([]byte("a,b\n"), ".csv", model.DataFormat{}); err == nil {
		t.Error("a header with no rows should be refused")
	}
	if _, err := Delimiter("::"); err == nil {
		t.Error("multi-character delimiter")
	}
}
