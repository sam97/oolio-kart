package couponclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sam97/oolio-kart/pkg/datasources/cache"
	"github.com/sam97/oolio-kart/pkg/helpers/logging"
	"github.com/sam97/oolio-kart/pkg/models"
)

// fakeCouponsService serves the coupons service API, answering with status for
// every request.
func fakeCouponsService(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/coupons/HAPPYHRS" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Request-Id"); got != "req-1" {
			t.Errorf("X-Request-Id = %q, want req-1", got)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL+"/", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return client, &calls
}

func TestClient(t *testing.T) {
	ctx := logging.WithRequestID(t.Context(), "req-1")

	t.Run("valid", func(t *testing.T) {
		client, _ := fakeCouponsService(t, http.StatusOK, `{"code":"HAPPYHRS","discountPercent":10}`)
		got, err := client.Validate(ctx, "HAPPYHRS")
		if err != nil || got != (models.Coupon{Code: "HAPPYHRS", DiscountPercent: 10}) {
			t.Errorf("Validate = %+v, %v", got, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		client, _ := fakeCouponsService(t, http.StatusNotFound, `{}`)
		if _, err := client.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponInvalid) {
			t.Errorf("err = %v, want ErrCouponInvalid", err)
		}
	})
	t.Run("loading", func(t *testing.T) {
		client, _ := fakeCouponsService(t, http.StatusServiceUnavailable, `{}`)
		if _, err := client.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponUnavailable) {
			t.Errorf("err = %v, want ErrCouponUnavailable", err)
		}
	})
	t.Run("bad body", func(t *testing.T) {
		client, _ := fakeCouponsService(t, http.StatusOK, `not json`)
		if _, err := client.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponUnavailable) {
			t.Errorf("err = %v, want ErrCouponUnavailable", err)
		}
	})
	t.Run("malformed code skips the service", func(t *testing.T) {
		client, calls := fakeCouponsService(t, http.StatusOK, `{}`)
		if _, err := client.Validate(ctx, "BAD/../CODE"); !errors.Is(err, models.ErrCouponInvalid) {
			t.Errorf("err = %v, want ErrCouponInvalid", err)
		}
		if *calls != 0 {
			t.Errorf("service called %d times", *calls)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		client, err := NewClient("http://127.0.0.1:1", 200*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Validate(ctx, "HAPPYHRS"); !errors.Is(err, models.ErrCouponUnavailable) {
			t.Errorf("err = %v, want ErrCouponUnavailable", err)
		}
	})
}

func TestNewClientRejectsBadURL(t *testing.T) {
	for _, raw := range []string{"", "localhost:8081", "ftp://host", "http://"} {
		if _, err := NewClient(raw, time.Second); err == nil {
			t.Errorf("NewClient(%q) succeeded", raw)
		}
	}
}

// stubValidator answers from a map and counts calls.
type stubValidator struct {
	answers map[string]error
	calls   int
}

func (s *stubValidator) Validate(_ context.Context, code string) (models.Coupon, error) {
	s.calls++
	if err := s.answers[code]; err != nil {
		return models.Coupon{}, err
	}
	return models.Coupon{Code: code, DiscountPercent: 10}, nil
}

func TestCached(t *testing.T) {
	ctx := t.Context()
	store, err := cache.NewMemory(10)
	if err != nil {
		t.Fatal(err)
	}
	next := &stubValidator{answers: map[string]error{
		"SUPER100": models.ErrCouponInvalid,
		"LOADING1": models.ErrCouponUnavailable,
	}}
	cached := NewCached(next, store, time.Minute, time.Minute, slog.New(slog.DiscardHandler))

	for range 3 {
		if got, err := cached.Validate(ctx, "HAPPYHRS"); err != nil || got.DiscountPercent != 10 {
			t.Fatalf("Validate(HAPPYHRS) = %+v, %v", got, err)
		}
		if _, err := cached.Validate(ctx, "SUPER100"); !errors.Is(err, models.ErrCouponInvalid) {
			t.Fatalf("Validate(SUPER100) err = %v", err)
		}
		if _, err := cached.Validate(ctx, "LOADING1"); !errors.Is(err, models.ErrCouponUnavailable) {
			t.Fatalf("Validate(LOADING1) err = %v", err)
		}
	}
	// Valid and invalid answers are fetched once; unavailable every time.
	if want := 1 + 1 + 3; next.calls != want {
		t.Errorf("wrapped validator called %d times, want %d", next.calls, want)
	}
}

func TestCachedZeroTTLDisablesCaching(t *testing.T) {
	store, err := cache.NewMemory(10)
	if err != nil {
		t.Fatal(err)
	}
	next := &stubValidator{answers: map[string]error{"SUPER100": models.ErrCouponInvalid}}
	cached := NewCached(next, store, 0, 0, slog.New(slog.DiscardHandler))

	for range 2 {
		cached.Validate(t.Context(), "HAPPYHRS")
		cached.Validate(t.Context(), "SUPER100")
	}
	if next.calls != 4 {
		t.Errorf("wrapped validator called %d times, want 4", next.calls)
	}
}
