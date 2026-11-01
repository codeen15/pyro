// Package auto detects an import's format (OpenAPI/Swagger, HAR, cURL,
// Postman) and runs the matching importer.
package auto

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"

	"github.com/codeen15/pyro/pkg/engine/importer"
	"github.com/codeen15/pyro/pkg/engine/importer/curl"
	"github.com/codeen15/pyro/pkg/engine/importer/har"
	"github.com/codeen15/pyro/pkg/engine/importer/openapi"
	"github.com/codeen15/pyro/pkg/engine/importer/postman"
)

// Parse imports data. filename (optional) names collections for formats
// that carry no name of their own (cURL, HAR without a page title).
func Parse(filename string, data []byte) (*importer.Bundle, error) {
	name := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	if filename == "" || name == "." {
		name = ""
	}
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return nil, errors.New("the file is empty")
	case curl.Looks(trimmed):
		return curl.Parse(name, trimmed)
	case openapi.Looks(trimmed):
		return openapi.Parse(trimmed)
	case trimmed[0] == '{' && har.Looks(trimmed):
		return har.Parse(name, trimmed)
	case trimmed[0] == '{':
		return postman.Parse(trimmed)
	}
	return nil, errors.New("unrecognised format: expected a Postman collection or environment, an OpenAPI/Swagger definition, a HAR file or cURL commands")
}
