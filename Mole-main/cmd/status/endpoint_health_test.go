package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpointHealthCheckerReportsHealthyResponseAndHidesQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request method = %q, want GET", r.Method)
		}
		if r.URL.Query().Get("token") != "private" {
			t.Errorf("request query was not preserved: %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	checker, err := NewEndpointHealthChecker([]string{server.URL + "/ready?token=private"})
	if err != nil {
		t.Fatalf("NewEndpointHealthChecker() error = %v", err)
	}

	checkedAt := time.Now()
	results := checker.Check(checkedAt)
	if len(results) != 1 {
		t.Fatalf("Check() returned %d results, want 1", len(results))
	}
	got := results[0]
	if !got.Healthy || got.Status != "up" || got.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected successful check: %+v", got)
	}
	if got.URL != server.URL+"/ready" || strings.Contains(got.URL, "private") {
		t.Fatalf("check URL leaked its query: %q", got.URL)
	}
	if got.CheckedAt != checkedAt {
		t.Fatalf("CheckedAt = %v, want %v", got.CheckedAt, checkedAt)
	}
	if got.LatencyMS < 0 {
		t.Fatalf("LatencyMS = %v, want non-negative latency", got.LatencyMS)
	}
}

func TestEndpointHealthCheckerReportsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	checker, err := NewEndpointHealthChecker([]string{server.URL})
	if err != nil {
		t.Fatalf("NewEndpointHealthChecker() error = %v", err)
	}
	got := checker.Check(time.Now())[0]
	if got.Healthy || got.Status != "down" || got.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected failed status check: %+v", got)
	}
	if got.Error != "HTTP 503" {
		t.Fatalf("Error = %q, want HTTP 503", got.Error)
	}
}

func TestEndpointHealthCheckerReportsNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	checker, err := NewEndpointHealthChecker([]string{server.URL})
	if err != nil {
		t.Fatalf("NewEndpointHealthChecker() error = %v", err)
	}
	got := checker.Check(time.Now())[0]
	if got.Healthy || got.Status != "down" || got.Error != "request failed" {
		t.Fatalf("unexpected network failure result: %+v", got)
	}
}

func TestEndpointHealthCheckerReportsTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker, err := NewEndpointHealthChecker([]string{server.URL})
	if err != nil {
		t.Fatalf("NewEndpointHealthChecker() error = %v", err)
	}
	checker.client.Timeout = 10 * time.Millisecond
	got := checker.Check(time.Now())[0]
	if got.Healthy || got.Error != "request timed out" {
		t.Fatalf("unexpected timeout result: %+v", got)
	}
}

func TestNewEndpointHealthCheckerValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name string
		urls []string
	}{
		{name: "unsupported scheme", urls: []string{"ftp://example.com"}},
		{name: "missing host", urls: []string{"https:///health"}},
		{name: "credentials", urls: []string{"https://user:pass@example.com/health"}},
		{name: "fragment", urls: []string{"https://example.com/health#section"}},
		{name: "invalid port", urls: []string{"https://example.com:65536/health"}},
		{name: "duplicate", urls: []string{"https://example.com/health", "https://example.com/health"}},
		{name: "too many", urls: makeEndpointURLs(maxEndpointChecks + 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewEndpointHealthChecker(tt.urls); err == nil {
				t.Fatal("NewEndpointHealthChecker() error = nil, want validation error")
			}
		})
	}
}

func TestEndpointHealthResultsAreMachineReadable(t *testing.T) {
	result := EndpointHealthStatus{
		URL:        "https://example.com/health",
		Healthy:    true,
		Status:     "up",
		StatusCode: http.StatusOK,
		LatencyMS:  1.5,
		CheckedAt:  time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(MetricsSnapshot{EndpointChecks: []EndpointHealthStatus{result}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"endpoint_checks":[`) ||
		!strings.Contains(string(data), `"latency_ms":1.5`) {
		t.Fatalf("endpoint check JSON missing expected fields: %s", data)
	}
}

func makeEndpointURLs(count int) []string {
	urls := make([]string, count)
	for i := range urls {
		urls[i] = "https://example.com/" + string(rune('a'+i))
	}
	return urls
}
