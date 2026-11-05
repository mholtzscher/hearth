package dbload

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

//go:embed report.html
var reportTemplateSource string

type reportTemplateData struct {
	Title string
	Data  template.JS
}

// RenderReport writes one self-contained offline HTML report from recorded
// evidence. Report data is embedded as JSON inside a non-executing script; the
// encoder escapes HTML-sensitive bytes and all rendered values are assigned as
// text. The file is written atomically beside the requested output path.
func RenderReport(outputHTML string, data ReportData) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode report data: %w", err)
	}
	parsed, err := template.New("report").Parse(reportTemplateSource)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	title := strings.TrimSpace(data.Title)
	if title == "" {
		title = "Database scale experiment report"
	}
	payload := reportTemplateData{
		Title: title,
		//nolint:gosec // encoding/json escapes HTML-sensitive bytes; the value is inert JSON.
		Data: template.JS(encoded),
	}
	directory := filepath.Dir(outputHTML)
	output, err := os.CreateTemp(directory, ".dbload-report-*.html")
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	temporary := output.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(temporary)
		}
	}()
	if renderErr := parsed.Execute(output, payload); renderErr != nil {
		_ = output.Close()
		return fmt.Errorf("render report: %w", renderErr)
	}
	if closeErr := output.Close(); closeErr != nil {
		return fmt.Errorf("close report file: %w", closeErr)
	}
	if renameErr := os.Rename(temporary, outputHTML); renameErr != nil {
		return fmt.Errorf("publish report: %w", renameErr)
	}
	renamed = true
	return nil
}
