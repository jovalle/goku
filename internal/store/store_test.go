package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
)

func seedConfig() model.Config {
	return model.Config{
		Links: map[string]string{"gh": "https://github.com", "g": "https://google.com"},
		Rules: []model.Rule{
			{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://www.reddit.com/r"},
			{Name: "gh", Type: "template", Pattern: "gh/{owner}/{name}", Redirect: "https://github.com/{owner}/{name}"},
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

func TestResolve_EmptyPath(t *testing.T) {
	s := New(seedConfig())
	_, err := s.Resolve("")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound for empty path, got %v", err)
	}
}

func TestResolve_ExactMatchWithSlash_FallsThrough(t *testing.T) {
	s := New(model.Config{Links: map[string]string{"gh": "https://github.com"}})
	_, err := s.Resolve("gh/org")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestResolve_Priority_ExactOverPrefix(t *testing.T) {
	s := New(model.Config{
		Links: map[string]string{"r": "https://exact.example.com"},
		Rules: []model.Rule{{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://reddit.com/r"}},
	})
	url, err := s.Resolve("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://exact.example.com" {
		t.Errorf("exact match should win, got %q", url)
	}
}

func TestNew_NilMaps(t *testing.T) {
	s := New(model.Config{})
	if s.Links() == nil {
		t.Error("Links() should not be nil")
	}
	if s.Rules() == nil {
		t.Error("Rules() should not be nil")
	}
}

func TestLinks_ReturnsCopy(t *testing.T) {
	s := New(seedConfig())
	links := s.Links()
	links["injected"] = "https://evil.example.com"
	if _, ok := s.Links()["injected"]; ok {
		t.Error("Links() should return a copy; mutation leaked")
	}
}

func TestRules_ReturnsCopy(t *testing.T) {
	s := New(seedConfig())
	rules := s.Rules()
	rules[0].Name = "MODIFIED"
	if s.Rules()[0].Name == "MODIFIED" {
		t.Error("Rules() should return a copy; mutation leaked")
	}
}

func TestAddLink(t *testing.T) {
	s := New(model.Config{})
	cfg := s.AddLink("docs", "https://docs.example.com")
	if cfg.Links["docs"] != "https://docs.example.com" {
		t.Error("AddLink did not return updated config")
	}
	url, err := s.Resolve("docs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://docs.example.com" {
		t.Errorf("got %q", url)
	}
}

func TestDeleteLink(t *testing.T) {
	s := New(seedConfig())
	cfg := s.DeleteLink("gh")
	if _, ok := cfg.Links["gh"]; ok {
		t.Error("DeleteLink did not remove link")
	}
	_, err := s.Resolve("gh")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Error("expected ErrNotFound after deleting link")
	}
}

func TestDeleteLink_NonExistent(t *testing.T) {
	s := New(seedConfig())
	before := len(s.Links())
	s.DeleteLink("nonexistent")
	if len(s.Links()) != before {
		t.Error("deleting non-existent link should be a no-op")
	}
}

func TestAddRule(t *testing.T) {
	s := New(model.Config{})
	rule := model.Rule{Name: "wiki", Type: "template", Pattern: "wiki/{topic}", Redirect: "https://en.wikipedia.org/wiki/{topic}"}
	cfg := s.AddRule(rule)
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}
	url, err := s.Resolve("wiki/Go_(programming_language)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://en.wikipedia.org/wiki/Go_(programming_language)" {
		t.Errorf("got %q", url)
	}
}

func TestDeleteRule(t *testing.T) {
	s := New(seedConfig())
	cfg := s.DeleteRule("reddit")
	for _, r := range cfg.Rules {
		if r.Name == "reddit" {
			t.Error("DeleteRule did not remove the rule")
		}
	}
	_, err := s.Resolve("r/golang")
	if !errors.Is(err, resolve.ErrNotFound) {
		t.Error("expected ErrNotFound after deleting prefix rule")
	}
}

func TestDeleteRule_NonExistent(t *testing.T) {
	s := New(seedConfig())
	before := len(s.Rules())
	s.DeleteRule("nonexistent")
	if len(s.Rules()) != before {
		t.Error("deleting non-existent rule should be a no-op")
	}
}

func TestUpdate(t *testing.T) {
	s := New(model.Config{})
	s.Update(model.Config{Links: map[string]string{"x": "https://x.com"}})
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
	if s.Links() == nil {
		t.Error("Links() should not be nil after Update with empty config")
	}
	if s.Rules() == nil {
		t.Error("Rules() should not be nil after Update with empty config")
	}
}

func TestConfig_ReturnsCopy(t *testing.T) {
	s := New(seedConfig())
	cfg := s.Config()
	cfg.Links["injected"] = "evil"
	if _, ok := s.Config().Links["injected"]; ok {
		t.Error("Config() should return a deep copy")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New(seedConfig())
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			s.Resolve("gh")
		}()
		go func() {
			defer wg.Done()
			s.Links()
		}()
		go func() {
			defer wg.Done()
			s.Rules()
		}()
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				s.AddLink("tmp", "https://tmp.example.com")
			} else {
				s.DeleteLink("tmp")
			}
		}(i)
	}
	wg.Wait()
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
