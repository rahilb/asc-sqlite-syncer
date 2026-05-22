package asc

import (
	"context"
	"strings"
)

// SubscriptionKey identifies an aggregated active-subscriptions bucket.
type SubscriptionKey struct {
	App          string
	Subscription string
	Country      string
}

// SubscriptionData is the aggregated result of one daily SUBSCRIPTION report.
type SubscriptionData struct {
	Date string
	// Active counts every subscriber the report marks as active, summing all
	// columns whose header begins with "Active" (standard price, free trial,
	// intro offers, etc.). This keeps the metric stable across report versions.
	Active map[SubscriptionKey]float64
}

// SubscriptionReport fetches and aggregates the latest available daily
// SUBSCRIPTION SUMMARY report for the vendor.
func (c *Client) SubscriptionReport(ctx context.Context, vendor, version string, lookback int) (*SubscriptionData, error) {
	rep, err := c.fetchLatestReport(ctx, reportParams{
		frequency:  "DAILY",
		reportType: "SUBSCRIPTION",
		subType:    "SUMMARY",
		vendor:     vendor,
		version:    version,
	}, lookback)
	if err != nil {
		return nil, err
	}
	return aggregateSubscriptions(rep.Date, rep.Rows), nil
}

// aggregateSubscriptions rolls report rows up into active-subscriber buckets.
func aggregateSubscriptions(date string, rows []map[string]string) *SubscriptionData {
	data := &SubscriptionData{
		Date:   date,
		Active: make(map[SubscriptionKey]float64),
	}

	for _, row := range rows {
		key := SubscriptionKey{
			App:          firstNonEmpty(row["App Name"], row["App Apple ID"]),
			Subscription: firstNonEmpty(row["Subscription Name"], row["Subscription Apple ID"]),
			Country:      firstNonEmpty(row["Country"], row["Country Code"]),
		}
		var active float64
		for col, val := range row {
			if strings.HasPrefix(col, "Active") {
				active += parseFloat(val)
			}
		}
		data.Active[key] += active
	}
	return data
}
