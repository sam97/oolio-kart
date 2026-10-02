package couponclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sam97/oolio-kart/internal/logging"
)

// maxResponseBytes caps how much of a coupons service response is read.
const maxResponseBytes = 4 << 10

// Client asks the coupons service whether a code is valid.
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("coupons URL %q must be an absolute http(s) URL", baseURL)
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) Validate(ctx context.Context, code string) (Coupon, error) {
	if !WellFormed(code) {
		return Coupon{}, ErrInvalid
	}

	endpoint := c.baseURL + "/v1/coupons/" + url.PathEscape(code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Coupon{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	if id := logging.RequestID(ctx); id != "" {
		req.Header.Set("X-Request-Id", id)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Coupon{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, maxResponseBytes)

	switch resp.StatusCode {
	case http.StatusOK:
		var found Coupon
		if err := json.NewDecoder(body).Decode(&found); err != nil {
			return Coupon{}, fmt.Errorf("%w: decode response: %w", ErrUnavailable, err)
		}
		return found, nil
	case http.StatusNotFound:
		return Coupon{}, ErrInvalid
	default:
		return Coupon{}, fmt.Errorf("%w: unexpected status %d", ErrUnavailable, resp.StatusCode)
	}
}
