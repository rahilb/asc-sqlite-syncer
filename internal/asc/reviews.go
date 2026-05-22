package asc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ReviewsData aggregates customer reviews for one app.
type ReviewsData struct {
	AppID   string
	AppName string
	// Total is the count of all reviews matching the query, reported by Apple's
	// paging metadata (does not require fetching every page).
	Total int
	// Sampled is the number of reviews actually fetched (bounded by ReviewsMax).
	Sampled int
	// RatingCounts maps star rating (1-5) to count over the sampled reviews.
	RatingCounts map[int]float64
	// RatingAvg is the mean rating over the sampled reviews.
	RatingAvg float64
}

type reviewsResponse struct {
	Data []struct {
		Attributes struct {
			Rating    int    `json:"rating"`
			Territory string `json:"territory"`
		} `json:"attributes"`
	} `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
	Meta struct {
		Paging struct {
			Total int `json:"total"`
		} `json:"paging"`
	} `json:"meta"`
}

// CustomerReviews fetches up to max reviews for an app, newest first, and
// aggregates the rating distribution and average.
func (c *Client) CustomerReviews(ctx context.Context, appID string, max int) (*ReviewsData, error) {
	data := &ReviewsData{
		AppID:        appID,
		AppName:      c.appName(ctx, appID),
		RatingCounts: map[int]float64{1: 0, 2: 0, 3: 0, 4: 0, 5: 0},
	}

	q := url.Values{}
	q.Set("limit", "200")
	q.Set("sort", "-createdDate")
	next := fmt.Sprintf("%s/v1/apps/%s/customerReviews?%s", c.baseURL, appID, q.Encode())

	var sum float64
	first := true
	for next != "" && data.Sampled < max {
		var page reviewsResponse
		if err := c.getJSON(ctx, next, &page); err != nil {
			return nil, fmt.Errorf("fetching reviews for app %s: %w", appID, err)
		}
		if first {
			data.Total = page.Meta.Paging.Total
			first = false
		}
		for _, r := range page.Data {
			rating := r.Attributes.Rating
			if rating >= 1 && rating <= 5 {
				data.RatingCounts[rating]++
				sum += float64(rating)
				data.Sampled++
			}
			if data.Sampled >= max {
				break
			}
		}
		next = page.Links.Next
	}

	if data.Total == 0 {
		data.Total = data.Sampled
	}
	if data.Sampled > 0 {
		data.RatingAvg = sum / float64(data.Sampled)
	}
	return data, nil
}

// appName fetches the app's display name; on any error it falls back to the ID.
func (c *Client) appName(ctx context.Context, appID string) string {
	var resp struct {
		Data struct {
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"data"`
	}
	url := fmt.Sprintf("%s/v1/apps/%s?fields[apps]=name", c.baseURL, appID)
	if err := c.getJSON(ctx, url, &resp); err != nil || resp.Data.Attributes.Name == "" {
		return appID
	}
	return resp.Data.Attributes.Name
}

// getJSON performs an authenticated GET and decodes a JSON body.
func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	resp, err := c.get(ctx, url, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}
