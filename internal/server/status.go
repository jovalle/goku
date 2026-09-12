package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func (c *aliasStatusChecker) statuses(ctx context.Context, aliases []model.Alias) []aliasStatusItem {
	now := time.Now().UTC()
	items := make([]aliasStatusItem, len(aliases))
	type probeRequest struct {
		probeURL string
	}

	requests := map[string]probeRequest{}
	results := map[string]aliasStatusProbe{}
	activeProbeURLs := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		activeProbeURLs[statusProbeURL(alias.Alias, alias.Destination)] = struct{}{}
	}
	c.mu.Lock()
	for probeURL := range c.cache {
		if _, active := activeProbeURLs[probeURL]; !active {
			delete(c.cache, probeURL)
		}
	}
	for _, alias := range aliases {
		probeURL := statusProbeURL(alias.Alias, alias.Destination)
		if cached, ok := c.cache[probeURL]; ok && now.Sub(cached.CheckedAt) < c.ttl {
			results[probeURL] = cached
			continue
		}
		requests[probeURL] = probeRequest{probeURL: probeURL}
	}
	c.mu.Unlock()

	if len(requests) > 0 {
		sem := make(chan struct{}, c.maxConcurrent)
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, req := range requests {
			wg.Go(func() {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}

				probe := c.check(ctx, req.probeURL)
				mu.Lock()
				results[req.probeURL] = probe
				mu.Unlock()

				c.mu.Lock()
				c.cache[req.probeURL] = probe
				c.mu.Unlock()
			})
		}
		wg.Wait()
	}

	for i, alias := range aliases {
		effectiveDestination := store.DestinationWithAliasDefaults(alias.Alias, alias.Destination)
		probeURL := statusProbeURL(alias.Alias, alias.Destination)
		probe, ok := results[probeURL]
		if !ok {
			probe = aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not checked",
				CheckedAt: now,
			}
		}
		if placeholderTokenPattern.MatchString(effectiveDestination) {
			probe = placeholderAwareProbe(effectiveDestination, probe)
		}
		_, refreshed := requests[probeURL]
		items[i] = aliasStatusItem{
			Alias:       alias.Alias,
			Destination: alias.Destination,
			ProbeURL:    probe.ProbeURL,
			State:       probe.State,
			Detail:      probe.Detail,
			StatusCode:  probe.StatusCode,
			CheckedAt:   probe.CheckedAt,
			Cached:      !refreshed,
		}
	}
	return items
}

func placeholderAwareProbe(destination string, probe aliasStatusProbe) aliasStatusProbe {
	if !placeholderTokenPattern.MatchString(destination) || probe.State != "offline" {
		return probe
	}
	probe.State = "warning"
	if probe.StatusCode > 0 {
		probe.Detail = fmt.Sprintf("template destination probe returned HTTP %d", probe.StatusCode)
	} else {
		probe.Detail = "template destination needs sample values"
	}
	return probe
}

func (c *aliasStatusChecker) check(parent context.Context, probeURL string) aliasStatusProbe {
	checkedAt := time.Now().UTC()
	if !isHTTPProbeURL(probeURL) {
		return aliasStatusProbe{
			ProbeURL:  probeURL,
			State:     "warning",
			Detail:    "unsupported destination scheme",
			CheckedAt: checkedAt,
		}
	}

	statusCode, err := c.request(parent, http.MethodHead, probeURL)
	if err != nil {
		statusCode, err = c.request(parent, http.MethodGet, probeURL)
		if err != nil {
			return aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not reachable",
				CheckedAt: checkedAt,
			}
		}
	}
	if statusCode == http.StatusMethodNotAllowed {
		statusCode, err = c.request(parent, http.MethodGet, probeURL)
		if err != nil {
			return aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not reachable",
				CheckedAt: checkedAt,
			}
		}
	}

	state, detail := classifyAliasStatus(statusCode)
	return aliasStatusProbe{
		ProbeURL:   probeURL,
		State:      state,
		Detail:     detail,
		StatusCode: statusCode,
		CheckedAt:  checkedAt,
	}
}

func (c *aliasStatusChecker) request(parent context.Context, method string, probeURL string) (int, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, probeURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "goku-link-status/1.0")
	if method == http.MethodGet {
		req.Header.Set("Range", "bytes=0-0")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func classifyAliasStatus(statusCode int) (string, string) {
	detail := fmt.Sprintf("HTTP %d", statusCode)
	switch {
	case statusCode >= 200 && statusCode < 400:
		return "online", detail
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return "online", detail
	case statusCode == http.StatusNotFound:
		return "offline", detail
	case statusCode >= 400:
		return "warning", detail
	default:
		return "warning", detail
	}
}

func statusProbeURL(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	probeURL := strings.TrimSpace(replacePlaceholderTokens(effectiveDestination, ""))
	if parsed, err := url.Parse(probeURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return probeURL
	}
	return store.NormalizeDestination(probeURL)
}
