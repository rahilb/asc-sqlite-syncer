package asc

import (
	"context"
	"strconv"
	"strings"
)

// SalesUnitKey identifies an aggregated units bucket.
type SalesUnitKey struct {
	App         string
	SKU         string
	Country     string
	ProductType string
}

// SalesProceedKey identifies an aggregated proceeds bucket. Proceeds are kept
// per currency because rows mix currencies that must not be summed together.
type SalesProceedKey struct {
	App      string
	SKU      string
	Currency string
}

// SalesData is the aggregated result of one daily sales report.
type SalesData struct {
	Date     string
	Units    map[SalesUnitKey]float64
	Proceeds map[SalesProceedKey]float64
}

// SalesReport fetches and aggregates the latest available daily SALES SUMMARY
// report for the vendor.
func (c *Client) SalesReport(ctx context.Context, vendor, version string, lookback int) (*SalesData, error) {
	rep, err := c.fetchLatestReport(ctx, reportParams{
		frequency:  "DAILY",
		reportType: "SALES",
		subType:    "SUMMARY",
		vendor:     vendor,
		version:    version,
	}, lookback)
	if err != nil {
		return nil, err
	}
	return aggregateSales(rep.Date, rep.Rows), nil
}

// aggregateSales rolls report rows up into the exported buckets.
func aggregateSales(date string, rows []map[string]string) *SalesData {
	data := &SalesData{
		Date:     date,
		Units:    make(map[SalesUnitKey]float64),
		Proceeds: make(map[SalesProceedKey]float64),
	}

	for _, row := range rows {
		app := firstNonEmpty(row["Title"], row["SKU"])
		sku := row["SKU"]
		units := parseFloat(row["Units"])

		data.Units[SalesUnitKey{
			App:         app,
			SKU:         sku,
			Country:     row["Country Code"],
			ProductType: row["Product Type Identifier"],
		}] += units

		// Developer Proceeds is per-unit, in the currency of proceeds.
		perUnit := parseFloat(row["Developer Proceeds"])
		if currency := row["Currency of Proceeds"]; currency != "" {
			data.Proceeds[SalesProceedKey{
				App:      app,
				SKU:      sku,
				Currency: currency,
			}] += perUnit * units
		}
	}
	return data
}

// parseFloat tolerates empty and whitespace-padded numeric fields.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
