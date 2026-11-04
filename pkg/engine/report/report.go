// Package report renders run summaries for CI: JUnit XML and JSON.
package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"

	"github.com/codeen15/pyro/pkg/engine/runner"
)

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      string      `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
	SystemOut string      `xml:"system-out,omitempty"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Skipped   *struct{}     `xml:"skipped,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// JUnit renders one testsuite per request (per iteration when there are
// several) with one testcase per test. A request that got no response or
// whose scripts failed becomes an error case.
func JUnit(name string, s *runner.Summary) ([]byte, error) {
	out := junitSuites{Name: name, Time: secs(s.Duration.Seconds())}
	for _, ir := range s.Results {
		suiteName := ir.Path
		if s.Iterations > 1 {
			suiteName = fmt.Sprintf("%s [iteration %d]", ir.Path, ir.Iteration+1)
		}
		suite := junitSuite{Name: suiteName}
		if ir.Note != "" {
			suite.Tests, suite.Skipped = 1, 1
			suite.Cases = []junitCase{{Name: ir.Name, Classname: ir.Path, Time: "0", Skipped: &struct{}{}}}
			out.Suites = append(out.Suites, suite)
			continue
		}
		r := ir.Result
		suite.Timestamp = r.StartedAt.UTC().Format("2006-01-02T15:04:05")
		reqTime := "0"
		if r.Response != nil {
			reqTime = secs(r.Response.Timings.Total.Seconds())
		}
		suite.Time = reqTime
		var problems []string
		if r.Error != "" {
			problems = append(problems, r.Error)
		}
		problems = append(problems, r.ScriptErrors...)
		for _, p := range problems {
			suite.Cases = append(suite.Cases, junitCase{Name: "request", Classname: ir.Path, Time: reqTime,
				Error: &junitProblem{Message: p, Body: p}})
			suite.Errors++
		}
		for _, t := range r.Tests {
			c := junitCase{Name: t.Name, Classname: ir.Path, Time: "0"}
			if !t.Passed {
				c.Failure = &junitProblem{Message: t.Error, Body: t.Error}
				suite.Failures++
			}
			suite.Cases = append(suite.Cases, c)
		}
		if len(suite.Cases) == 0 {
			// A request with no tests still shows up as a passing case.
			suite.Cases = append(suite.Cases, junitCase{Name: "request", Classname: ir.Path, Time: reqTime})
		}
		for _, l := range r.Logs {
			suite.SystemOut += l.Level + ": " + l.Message + "\n"
		}
		suite.Tests = len(suite.Cases)
		out.Tests += suite.Tests
		out.Failures += suite.Failures
		out.Errors += suite.Errors
		out.Suites = append(out.Suites, suite)
	}
	b, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

// JSON renders the full summary, including every response.
func JSON(s *runner.Summary) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

func secs(f float64) string { return fmt.Sprintf("%.3f", f) }

// JSONIndent renders any report as indented JSON.
func JSONIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }
