package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxEndpointChecks    = 10
	endpointCheckTimeout = 3 * time.Second
)

type EndpointHealthStatus struct {
	URL        string    `json:"url"`
	Healthy    bool      `json:"healthy"`
	Status     string    `json:"status"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMS  float64   `json:"latency_ms"`
	CheckedAt  time.Time `json:"checked_at"`
	Error      string    `json:"error,omitempty"`
}

type endpointTarget struct {
	requestURL *url.URL
	displayURL string
}

type EndpointHealthChecker struct {
	targets []endpointTarget
	client  *http.Client
}

func NewEndpointHealthChecker(rawURLs []string) (*EndpointHealthChecker, error) {
	if len(rawURLs) > maxEndpointChecks {
		return nil, fmt.Errorf("at most %d endpoint URLs may be configured", maxEndpointChecks)
	}

	checker := &EndpointHealthChecker{
		client: &http.Client{Timeout: endpointCheckTimeout},
	}
	seen := make(map[string]struct{}, len(rawURLs))
	for _, rawURL := range rawURLs {
		target, err := parseEndpointTarget(rawURL)
		if err != nil {
			return nil, err
		}
		key := target.requestURL.String()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate endpoint URL: %s", target.displayURL)
		}
		seen[key] = struct{}{}
		checker.targets = append(checker.targets, target)
	}
	return checker, nil
}

func parseEndpointTarget(raw string) (endpointTarget, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return endpointTarget{}, errors.New("invalid endpoint URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return endpointTarget{}, errors.New("endpoint URL must use http or https and include a host")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return endpointTarget{}, errors.New("endpoint URL must not include credentials, fragments, or opaque data")
	}
	if port := parsed.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return endpointTarget{}, errors.New("endpoint URL port must be between 1 and 65535")
		}
	}

	displayURL := *parsed
	displayURL.RawQuery = ""
	displayURL.ForceQuery = false
	displayURL.Fragment = ""

	return endpointTarget{
		requestURL: parsed,
		displayURL: displayURL.String(),
	}, nil
}

func (c *EndpointHealthChecker) Check(now time.Time) []EndpointHealthStatus {
	if c == nil || len(c.targets) == 0 {
		return nil
	}

	results := make([]EndpointHealthStatus, len(c.targets))
	var wg sync.WaitGroup
	for i, target := range c.targets {
		wg.Go(func() {
			results[i] = c.checkTarget(now, target)
		})
	}
	wg.Wait()
	return results
}

func (c *EndpointHealthChecker) checkTarget(now time.Time, target endpointTarget) EndpointHealthStatus {
	result := EndpointHealthStatus{
		URL:       target.displayURL,
		Status:    "down",
		CheckedAt: now,
	}

	started := time.Now()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target.requestURL.String(), nil)
	if err == nil {
		var response *http.Response
		response, err = c.client.Do(req)
		if err == nil {
			result.StatusCode = response.StatusCode
			_ = response.Body.Close()
			result.Healthy = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
			if !result.Healthy {
				result.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
			}
		}
	}
	result.LatencyMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			result.Error = "request timed out"
		} else {
			result.Error = "request failed"
		}
		return result
	}
	if result.Healthy {
		result.Status = "up"
	}
	return result
}
