package store

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
)

func seedConfig() model.Config {
	return model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "g", Destination: "https://google.com"},
			{Alias: "r/{rest...}", Destination: "https://www.reddit.com/r/{rest...}"},
			{Alias: "gh/{owner}/{name}", Destination: "https://github.com/{owner}/{name}"},
		},
	}
}

func TestResolve_ExactMatch(t *testing.T) {
	s := New(seedConfig())
	url, err := s.Resolve("gh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com" {
		t.Errorf("got %q, want %q", url, "https://github.com")
	}
}

func TestResolve_PrefixRule(t *testing.T) {
	s := New(seedConfig())
	url, err := s.Resolve("r/golang")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://www.reddit.com/r/golang" {
		t.Errorf("got %q, want %q", url, "https://www.reddit.com/r/golang")
	}
}

func TestResolve_PrefixRule_DeepPath(t *testing.T) {
	s := New(seedConfig())
	url, err := s.Resolve("r/golang/comments/abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://www.reddit.com/r/golang/comments/abc" {
		t.Errorf("got %q, want %q", url, "https://www.reddit.com/r/golang/comments/abc")
	}
}

func TestResolve_TemplateRule(t *testing.T) {
	s := New(seedConfig())
	url, err := s.Resolve("gh/jovalle/goku")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/jovalle/goku" {
		t.Errorf("got %q, want %q", url, "https://github.com/jovalle/goku")
	}
}

func TestResolve_TemplateRuleWithPlaceholderDefaults(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{
			{Alias: "gh/{owner:=jovalle}/{repo}", Destination: "https://github.com/{owner}/{repo}"},
		},
	})
	url, err := s.Resolve("gh/openai/codex")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/openai/codex" {
		t.Errorf("got %q, want %q", url, "https://github.com/openai/codex")
	}
}

func TestResolve_TemplateRule_NoMatch(t *testing.T) {
	s := New(seedConfig())
	_, err := s.Resolve("gh/a/b/c")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	s := New(seedConfig())
	_, err := s.Resolve("nosuch")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateAlias_RequiresHTTPDestination(t *testing.T) {
	tests := []struct {
		name        string
		destination string
	}{
		{name: "relative path", destination: "/docs"},
		{name: "unsupported scheme", destination: "mailto:team@example.com"},
		{name: "contains spaces", destination: "https://example.com/a b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateAlias("docs", tt.destination); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	if err := ValidateAlias("gh/{owner}/{repo}", "https://github.com/{owner}/{repo}"); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if err := ValidateAlias("gh/{owner}/{repo}", "github.com/{owner}/{repo}"); err != nil {
		t.Fatalf("unexpected inferred HTTPS validation error: %v", err)
	}
	if err := ValidateAlias("gh/{owner:=jovalle}/{repo}", "https://github.com/{owner}/{repo}"); err != nil {
		t.Fatalf("unexpected default placeholder validation error: %v", err)
	}
	if err := ValidateAlias("gh/{:=goku}", "https://github.com/jovalle/{}"); err != nil {
		t.Fatalf("unexpected anonymous default placeholder validation error: %v", err)
	}
	if err := ValidateAlias("gh/{owner:jovalle}/{repo}", "https://github.com/{owner}/{repo}"); err == nil {
		t.Fatal("expected invalid default placeholder syntax error")
	}
	if err := ValidateAlias("gh/{owner}/{repo}", "https://github.com/{owner:=jovalle}/{repo}"); err == nil {
		t.Fatal("expected destination default placeholder validation error")
	}
}

func TestNormalizeAliasAndDestination_KeepsDefaultsOnAliasOnly(t *testing.T) {
	alias, destination, err := NormalizeAliasAndDestination("gh/{owner:=jovalle}/{repo}", "https://github.com/{owner}/{repo}")
	if err != nil {
		t.Fatalf("NormalizeAliasAndDestination default error: %v", err)
	}
	if alias != "gh/{owner:=jovalle}/{repo}" {
		t.Fatalf("alias = %q", alias)
	}
	if destination != "https://github.com/{owner}/{repo}" {
		t.Fatalf("destination = %q", destination)
	}

	alias, destination, err = NormalizeAliasAndDestination("repo/{:=goku}", "https://github.com/jovalle/{}")
	if err != nil {
		t.Fatalf("NormalizeAliasAndDestination anonymous default error: %v", err)
	}
	if alias != "repo/{:=goku}" {
		t.Fatalf("anonymous alias default = %q", alias)
	}
	if destination != "https://github.com/jovalle/{}" {
		t.Fatalf("anonymous destination default = %q", destination)
	}
}

func TestNormalizeAliasAndDestination_RejectsDestinationDefaults(t *testing.T) {
	_, _, err := NormalizeAliasAndDestination("gh/{owner}/{repo}", "https://github.com/{owner:=jovalle}/{repo}")
	if err == nil {
		t.Fatal("expected destination default error")
	}
	if !strings.Contains(err.Error(), "defined in alias") {
		t.Fatalf("error = %q, want alias-only guidance", err.Error())
	}
}

func TestStripPlaceholderDefaults(t *testing.T) {
	tests := map[string]string{
		"gh/{owner:=jovalle}/{repo:=goku}": "gh/{owner}/{repo}",
		"repo/{:=goku}":                    "repo/{}",
		"r/{rest...:=selfhosted}":          "r/{rest...}",
	}

	for input, want := range tests {
		if got := StripPlaceholderDefaults(input); got != want {
			t.Fatalf("StripPlaceholderDefaults(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAddAlias_InheritsPlaceholderDefaults(t *testing.T) {
	s := New(model.Config{})
	cfg, err := s.AddAlias("gh/{owner:=jovalle}/{repo}", "https://github.com/{owner}/{repo}")
	if err != nil {
		t.Fatalf("AddAlias error: %v", err)
	}
	got, ok := findAlias(cfg.Aliases, "gh/{owner:=jovalle}/{repo}")
	if !ok {
		t.Fatalf("expected inherited alias default, aliases = %#v", cfg.Aliases)
	}
	if got.Destination != "https://github.com/{owner}/{repo}" {
		t.Fatalf("destination = %q", got.Destination)
	}

	cfg, err = s.AddAlias("repo/{:=goku}", "https://github.com/jovalle/{}")
	if err != nil {
		t.Fatalf("AddAlias anonymous default error: %v", err)
	}
	got, ok = findAlias(cfg.Aliases, "repo/{:=goku}")
	if !ok {
		t.Fatalf("expected inherited anonymous alias default, aliases = %#v", cfg.Aliases)
	}
	if got.Destination != "https://github.com/jovalle/{}" {
		t.Fatalf("anonymous destination = %q", got.Destination)
	}
}

func TestAddAlias_NormalizesMissingProtocolToHTTPS(t *testing.T) {
	s := New(model.Config{})
	cfg, err := s.AddAlias("docs", "docs.example.com/path")
	if err != nil {
		t.Fatalf("AddAlias error: %v", err)
	}
	got, ok := findAlias(cfg.Aliases, "docs")
	if !ok {
		t.Fatal("expected docs alias")
	}
	if got.Destination != "https://docs.example.com/path" {
		t.Fatalf("destination = %q", got.Destination)
	}

	cfg, err = s.AddAlias("local", "localhost:3000/app")
	if err != nil {
		t.Fatalf("AddAlias localhost error: %v", err)
	}
	got, ok = findAlias(cfg.Aliases, "local")
	if !ok {
		t.Fatal("expected local alias")
	}
	if got.Destination != "http://localhost:3000/app" {
		t.Fatalf("localhost destination = %q", got.Destination)
	}

	cfg, err = s.AddAlias("ip", "127.0.0.1:8080/app")
	if err != nil {
		t.Fatalf("AddAlias IP error: %v", err)
	}
	got, ok = findAlias(cfg.Aliases, "ip")
	if !ok {
		t.Fatal("expected ip alias")
	}
	if got.Destination != "http://127.0.0.1:8080/app" {
		t.Fatalf("IP destination = %q", got.Destination)
	}

	cfg, err = s.AddAlias("ipv6", "[::1]:8080/app")
	if err != nil {
		t.Fatalf("AddAlias IPv6 error: %v", err)
	}
	got, ok = findAlias(cfg.Aliases, "ipv6")
	if !ok {
		t.Fatal("expected ipv6 alias")
	}
	if got.Destination != "http://[::1]:8080/app" {
		t.Fatalf("IPv6 destination = %q", got.Destination)
	}

	cfg, err = s.AddAlias("bare-ipv6", "2001:db8::1/app")
	if err != nil {
		t.Fatalf("AddAlias bare IPv6 error: %v", err)
	}
	got, ok = findAlias(cfg.Aliases, "bare-ipv6")
	if !ok {
		t.Fatal("expected bare-ipv6 alias")
	}
	if got.Destination != "http://[2001:db8::1]/app" {
		t.Fatalf("bare IPv6 destination = %q", got.Destination)
	}
}

func TestResolve_EmptyPath(t *testing.T) {
	s := New(seedConfig())
	_, err := s.Resolve("")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound for empty path, got %v", err)
	}
}

func TestResolve_ExactMatchWithSlash_FallsThrough(t *testing.T) {
	s := New(model.Config{Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}}})
	_, err := s.Resolve("gh/org")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestResolve_Priority_ExactOverPrefix(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{
			{Alias: "r", Destination: "https://exact.example.com"},
			{Alias: "r/{rest...}", Destination: "https://reddit.com/r/{rest...}"},
		},
	})
	url, err := s.Resolve("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://exact.example.com" {
		t.Errorf("exact match should win, got %q", url)
	}
}

func TestResolve_Priority_ExactOverFallbackRegardlessOfOrder(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{
			{Alias: "{query}", Destination: "https://{query}.example.com"},
			{Alias: "websecure-sonarr", Destination: "https://sonarr.example.invalid"},
		},
	})

	url, err := s.Resolve("websecure-sonarr")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://sonarr.example.invalid" {
		t.Fatalf("exact alias should win over fallback, got %q", url)
	}
}

func TestResolve_Priority_SpecificTemplateOverFallback(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{
			{Alias: "{query...}", Destination: "https://{query...}.example.com"},
			{Alias: "r/{rest...}", Destination: "https://www.reddit.com/r/{rest...}"},
		},
	})

	url, err := s.Resolve("r/golang")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://www.reddit.com/r/golang" {
		t.Fatalf("specific template should win over fallback, got %q", url)
	}
}

func TestUpsertAlias_PreservesEnabledState(t *testing.T) {
	s := New(model.Config{})

	cfg, err := s.UpsertAlias(model.Alias{
		Alias:       "docs",
		Destination: "https://docs.example.com",
		Enabled:     model.BoolPtr(false),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(cfg.Aliases))
	}
	if cfg.Aliases[0].IsEnabled() {
		t.Fatal("expected imported alias to stay disabled")
	}

	_, err = s.Resolve("docs")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("disabled alias should not resolve, got %v", err)
	}
}

func TestDeleteAliases(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "docs", Destination: "https://docs.example.com"},
			{Alias: "wiki", Destination: "https://wikipedia.org"},
		},
	})

	cfg := s.DeleteAliases([]string{"gh", "wiki", "missing"})
	if len(cfg.Aliases) != 1 {
		t.Fatalf("expected 1 alias after delete, got %d", len(cfg.Aliases))
	}
	if cfg.Aliases[0].Alias != "docs" {
		t.Fatalf("remaining alias = %q, want docs", cfg.Aliases[0].Alias)
	}

	if _, err := s.Resolve("gh"); !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("deleted alias gh should not resolve, got %v", err)
	}
	if _, err := s.Resolve("wiki"); !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("deleted alias wiki should not resolve, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	s := New(model.Config{})
	s.Update(model.Config{Aliases: []model.Alias{{Alias: "x", Destination: "https://x.com"}}})
	url, err := s.Resolve("x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://x.com" {
		t.Errorf("got %q", url)
	}
}

func TestUpdate_NilMaps(t *testing.T) {
	s := New(seedConfig())
	s.Update(model.Config{})
	if s.Aliases() == nil {
		t.Error("Aliases() should not be nil after Update with empty config")
	}
}

func TestConfig_ReturnsCopy(t *testing.T) {
	s := New(seedConfig())
	cfg := s.Config()
	cfg.Aliases[0].Alias = "injected"
	if _, ok := findAlias(s.Config().Aliases, "injected"); ok {
		t.Error("Config() should return a deep copy")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New(seedConfig())
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.Resolve("gh")
		}()
		go func() {
			defer wg.Done()
			s.Aliases()
		}()
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				s.AddAlias("tmp", "https://tmp.example.com")
			} else {
				s.DeleteAlias("tmp")
			}
		}(i)
	}
	wg.Wait()
}

func findAlias(aliases []model.Alias, alias string) (model.Alias, bool) {
	for _, a := range aliases {
		if a.Alias == alias {
			return a, true
		}
	}
	return model.Alias{}, false
}

func TestResolve_DisabledAliasIsIgnored(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(false)}},
	})

	_, err := s.Resolve("gh")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for disabled alias, got %v", err)
	}
}

func TestSetAliasEnabled(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})

	if _, err := s.SetAliasEnabled("gh", false); err != nil {
		t.Fatalf("SetAliasEnabled disable failed: %v", err)
	}
	if _, err := s.Resolve("gh"); !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("expected disabled alias to stop resolving, got %v", err)
	}

	if _, err := s.SetAliasEnabled("gh", true); err != nil {
		t.Fatalf("SetAliasEnabled enable failed: %v", err)
	}
	if got, err := s.Resolve("gh"); err != nil || got != "https://github.com" {
		t.Fatalf("expected resolved destination after re-enable, got %q, err=%v", got, err)
	}
}

func TestUpdateAlias(t *testing.T) {
	s := New(model.Config{
		Aliases: []model.Alias{{Alias: "docs", Destination: "https://docs.example.com"}},
	})

	_, err := s.UpdateAlias("docs", "docs2", "https://docs2.example.com", false)
	if err != nil {
		t.Fatalf("UpdateAlias failed: %v", err)
	}

	if _, err := s.Resolve("docs"); !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("old alias should not resolve after rename, got %v", err)
	}
	if _, err := s.Resolve("docs2"); !errors.Is(err, resolve.ErrNotFound) {
		t.Fatalf("renamed alias is disabled and should not resolve, got %v", err)
	}
}
