package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDoRetriesToSuccess(t *testing.T) {
	ctx := context.Background()
	b := Backoff{Attempts: 3, Base: time.Millisecond, Jitter: false}
	calls := 0
	err := Do(ctx, b, func() error {
		calls++
		if calls < 3 {
			return errors.New("fail")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("want 3 calls success, got %v calls=%d", err, calls)
	}
}

func TestDoExhausts(t *testing.T) {
	ctx := context.Background()
	b := Backoff{Attempts: 2, Base: time.Millisecond, Jitter: false, Max: time.Millisecond}
	calls := 0
	err := Do(ctx, b, func() error { calls++; return errors.New("always") })
	if err == nil || calls != 2 {
		t.Fatalf("exhaust: %v calls=%d", err, calls)
	}
}

func TestDoContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := Backoff{Attempts: 5, Base: 50 * time.Millisecond}
	err := Do(ctx, b, func() error { return errors.New("x") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestDoDefaults(t *testing.T) {
	ctx := context.Background()
	calls := 0
	err := Do(ctx, Backoff{}, func() error {
		calls++
		if calls == 1 {
			return errors.New("first")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetryAfter(t *testing.T) {
	if RetryAfter(nil) != 0 {
		t.Fatal("nil should be 0")
	}
	h := http.Header{}
	h.Set("Retry-After", "120")
	resp := &http.Response{Header: h}
	if RetryAfter(resp) != 120*time.Second {
		t.Fatalf("want 120s got %v", RetryAfter(resp))
	}
	if RetryAfter(&http.Response{Header: http.Header{}}) != 0 {
		t.Fatal("missing should be 0")
	}
	h2 := http.Header{}
	h2.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	resp2 := &http.Response{Header: h2}
	if d := RetryAfter(resp2); d <= 0 || d > 5*time.Second {
		t.Fatalf("http-date RetryAfter out of range: %v", d)
	}
}
