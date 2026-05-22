package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rahilb/asc-sqlite-syncer/internal/asc"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func countSalesUnits(t *testing.T, st *Store, date string) (rows int, total float64) {
	t.Helper()
	err := st.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(units),0) FROM sales_units WHERE report_date=?`, date,
	).Scan(&rows, &total)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return rows, total
}

func TestReplaceSalesRestatement(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	date := "2026-05-20"

	first := &asc.SalesData{
		Units: map[asc.SalesUnitKey]float64{
			{App: "A", SKU: "s", Country: "US", ProductType: "1F"}: 10,
			{App: "A", SKU: "s", Country: "GB", ProductType: "1F"}: 5,
		},
	}
	if err := st.ReplaceSales(ctx, date, first); err != nil {
		t.Fatalf("replace 1: %v", err)
	}
	if rows, total := countSalesUnits(t, st, date); rows != 2 || total != 15 {
		t.Fatalf("after first: rows=%d total=%v, want 2/15", rows, total)
	}

	// Restatement: US revised up, GB dropped from the report entirely.
	second := &asc.SalesData{
		Units: map[asc.SalesUnitKey]float64{
			{App: "A", SKU: "s", Country: "US", ProductType: "1F"}: 12,
		},
	}
	if err := st.ReplaceSales(ctx, date, second); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	if rows, total := countSalesUnits(t, st, date); rows != 1 || total != 12 {
		t.Fatalf("after restatement: rows=%d total=%v, want 1/12 (GB must be gone)", rows, total)
	}
}

func TestBackfillFlag(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	if ok, err := st.IsBackfilled(ctx); err != nil || ok {
		t.Fatalf("IsBackfilled = %v,%v; want false,nil", ok, err)
	}
	if err := st.SetBackfilled(ctx); err != nil {
		t.Fatalf("SetBackfilled: %v", err)
	}
	if ok, err := st.IsBackfilled(ctx); err != nil || !ok {
		t.Fatalf("IsBackfilled = %v,%v; want true,nil", ok, err)
	}
}

func TestReplaceReviews(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	date := "2026-05-22"

	data := []*asc.ReviewsData{{
		AppName:      "MyApp",
		Total:        1000,
		RatingAvg:    4.5,
		RatingCounts: map[int]float64{1: 10, 2: 20, 3: 30, 4: 40, 5: 50},
	}}
	if err := st.ReplaceReviews(ctx, date, data); err != nil {
		t.Fatalf("replace reviews: %v", err)
	}

	var total int
	var avg float64
	if err := st.db.QueryRow(
		`SELECT total, rating_avg FROM reviews WHERE snapshot_date=? AND app=?`, date, "MyApp",
	).Scan(&total, &avg); err != nil {
		t.Fatalf("query reviews: %v", err)
	}
	if total != 1000 || avg != 4.5 {
		t.Fatalf("reviews = %d/%v, want 1000/4.5", total, avg)
	}

	var ratingRows int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM review_ratings WHERE snapshot_date=?`, date,
	).Scan(&ratingRows); err != nil {
		t.Fatalf("query ratings: %v", err)
	}
	if ratingRows != 5 {
		t.Fatalf("rating rows = %d, want 5", ratingRows)
	}
}
