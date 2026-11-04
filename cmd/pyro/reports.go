package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// Report files and uploads shared by pyro run and pyro load.

type reportFlags struct {
	html, xlsx, csv  string
	reportTo, projID string
}

func (r *reportFlags) register(f *pflag.FlagSet, withHTML bool) {
	if withHTML {
		f.StringVar(&r.html, "html", "", "write a printable HTML report (open it and print to PDF)")
	}
	f.StringVar(&r.xlsx, "xlsx", "", "write an Excel workbook (summary, results, failures)")
	f.StringVar(&r.csv, "csv", "", "write the results table as CSV")
	f.StringVar(&r.reportTo, "report-to", "", "upload the report to a Pyro team server, e.g. https://pyro.example.com (token: $PYRO_TOKEN)")
	f.StringVar(&r.projID, "project", "", "with --report-to: the project ID (from the project's address, /p/<id>)")
}

func (r *reportFlags) check() error {
	if r.reportTo == "" {
		return nil
	}
	if r.projID == "" {
		return errors.New("--report-to needs --project (the ID in the project's address, /p/<id>)")
	}
	if os.Getenv("PYRO_TOKEN") == "" {
		return errors.New("--report-to needs an API token in $PYRO_TOKEN (Account → API tokens on the team server)")
	}
	return nil
}

// write saves one report file if its flag was given.
func writeReport(path string, render func() ([]byte, error)) error {
	if path == "" {
		return nil
	}
	b, err := render()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("Report: %s\n", path)
	return nil
}

// upload sends a report to a team server and prints its address.
func (r *reportFlags) upload(body any) error {
	if r.reportTo == "" {
		return nil
	}
	base := strings.TrimSuffix(r.reportTo, "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("--report-to %q isn't an http(s) address", r.reportTo)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/projects/"+url.PathEscape(r.projID)+"/reports", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("PYRO_TOKEN"))
	res, err := (&http.Client{Timeout: time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("upload report: %w", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusCreated {
		var e struct{ Error string }
		_ = json.Unmarshal(out, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return fmt.Errorf("upload report: %s", e.Error)
	}
	var created struct{ ID, Kind string }
	_ = json.Unmarshal(out, &created)
	fmt.Printf("Uploaded: %s/p/%s/reports/%s\n", base, url.PathEscape(r.projID), created.ID)
	return nil
}
