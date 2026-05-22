package asc

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// reportParams identifies a single App Store Connect report request.
type reportParams struct {
	frequency  string // e.g. DAILY
	reportType string // e.g. SALES, SUBSCRIPTION
	subType    string // e.g. SUMMARY
	vendor     string
	version    string
}

// fetchReportForDate fetches one report for an exact date. found is false when
// Apple reports no data for that date (HTTP 404), which is normal for some days.
func (c *Client) fetchReportForDate(ctx context.Context, p reportParams, date string) (rows []map[string]string, found bool, err error) {
	q := url.Values{}
	q.Set("filter[frequency]", p.frequency)
	q.Set("filter[reportType]", p.reportType)
	q.Set("filter[reportSubType]", p.subType)
	q.Set("filter[vendorNumber]", p.vendor)
	q.Set("filter[reportDate]", date)
	if p.version != "" {
		q.Set("filter[version]", p.version)
	}
	endpoint := c.baseURL + "/v1/salesReports?" + q.Encode()

	resp, err := c.get(ctx, endpoint, "application/a-gzip")
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		rows, err = parseTSVGzip(resp.Body)
		if err != nil {
			return nil, false, err
		}
		return rows, true, nil
	case http.StatusNotFound:
		// No report available for this date.
		return nil, false, nil
	default:
		return nil, false, statusError(resp)
	}
}

// parseTSVGzip gunzips a report body and parses it into header-keyed rows.
func parseTSVGzip(body io.Reader) ([]map[string]string, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return nil, fmt.Errorf("gunzip report: %w", err)
	}
	defer gz.Close()

	r := csv.NewReader(gz)
	r.Comma = '\t'
	r.LazyQuotes = true
	r.FieldsPerRecord = -1 // Reports have a trailing/ragged column count.

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing report TSV: %w", err)
	}
	if len(records) < 1 {
		return nil, nil
	}

	header := records[0]
	rows := make([]map[string]string, 0, len(records)-1)
	for _, rec := range records[1:] {
		row := make(map[string]string, len(header))
		for i, col := range header {
			if i < len(rec) {
				row[col] = rec[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
