package asc

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func gzipTSV(t *testing.T, tsv string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(tsv)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestParseTSVGzip(t *testing.T) {
	tsv := "A\tB\tC\n1\tfoo\t3\n4\tbar\t6\n"
	rows, err := parseTSVGzip(gzipTSV(t, tsv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["A"] != "1" || rows[0]["B"] != "foo" || rows[1]["C"] != "6" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
}

func TestAggregateSales(t *testing.T) {
	rows := []map[string]string{
		{"Title": "MyApp", "SKU": "sku1", "Country Code": "US", "Product Type Identifier": "1F",
			"Units": "10", "Developer Proceeds": "0.70", "Currency of Proceeds": "USD"},
		{"Title": "MyApp", "SKU": "sku1", "Country Code": "US", "Product Type Identifier": "1F",
			"Units": "5", "Developer Proceeds": "0.70", "Currency of Proceeds": "USD"},
		{"Title": "MyApp", "SKU": "sku1", "Country Code": "GB", "Product Type Identifier": "1F",
			"Units": "3", "Developer Proceeds": "0.60", "Currency of Proceeds": "GBP"},
	}
	d := aggregateSales("2026-05-20", rows)

	if got := d.Units[SalesUnitKey{"MyApp", "sku1", "US", "1F"}]; got != 15 {
		t.Errorf("US units = %v, want 15", got)
	}
	if got := d.Proceeds[SalesProceedKey{"MyApp", "sku1", "USD"}]; got != 10.5 {
		t.Errorf("USD proceeds = %v, want 10.5", got)
	}
	if got := d.Proceeds[SalesProceedKey{"MyApp", "sku1", "GBP"}]; got < 1.79 || got > 1.81 {
		t.Errorf("GBP proceeds = %v, want ~1.8", got)
	}
}

func TestAggregateSubscriptions(t *testing.T) {
	rows := []map[string]string{
		{"App Name": "MyApp", "Subscription Name": "Pro", "Country": "US",
			"Active Standard Price Subscriptions": "100",
			"Active Free Trial Introductory Offer Subscriptions": "20",
			"Customer Price": "9.99"},
		{"App Name": "MyApp", "Subscription Name": "Pro", "Country": "US",
			"Active Standard Price Subscriptions": "5"},
	}
	d := aggregateSubscriptions("2026-05-20", rows)
	if got := d.Active[SubscriptionKey{"MyApp", "Pro", "US"}]; got != 125 {
		t.Errorf("active subs = %v, want 125", got)
	}
}
