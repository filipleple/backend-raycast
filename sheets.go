package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// sheetsHTTPClient bounds every Google fetch — a hung request must not wedge an
// admin sync.
var sheetsHTTPClient = &http.Client{Timeout: 30 * time.Second}

// fetchSheetCSV downloads one tab of a Google Sheet as CSV via the public
// export endpoint. It requires the sheet to be shared "anyone with the link can
// view" — no OAuth, no API key. A private sheet returns Google's HTML sign-in
// page instead of CSV, which we detect and report.
//
// This is the single seam for sheet access: swapping in a service-account
// Sheets API call later means reimplementing only this function.
func fetchSheetCSV(sheetID, gid string) ([]byte, error) {
	if sheetID == "" || gid == "" {
		return nil, fmt.Errorf("sheet id and gid must both be set")
	}
	endpoint := fmt.Sprintf(
		"https://docs.google.com/spreadsheets/d/%s/export?format=csv&gid=%s",
		url.PathEscape(sheetID), url.QueryEscape(gid),
	)

	resp, err := sheetsHTTPClient.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetching sheet: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sheet returned HTTP %d (is it shared publicly?)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("reading sheet body: %w", err)
	}

	// A private/misconfigured sheet serves an HTML login page with status 200.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return nil, fmt.Errorf("got HTML, not CSV — check the sheet is shared 'anyone with the link' and the gid is correct")
	}
	// Must parse as CSV before we trust it as content.
	if _, err := csv.NewReader(bytes.NewReader(body)).ReadAll(); err != nil {
		return nil, fmt.Errorf("sheet is not valid CSV: %w", err)
	}
	return body, nil
}
