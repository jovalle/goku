package model

import "testing"

func TestRule_String(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{
			name: "prefix rule",
			rule: Rule{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://www.reddit.com/r"},
			want: "[prefix] reddit: r -> https://www.reddit.com/r",
		},
		{
			name: "template rule",
			rule: Rule{Name: "gh", Type: "template", Pattern: "gh/{owner}/{name}", Redirect: "https://github.com/{owner}/{name}"},
			want: "[template] gh: gh/{owner}/{name} -> https://github.com/{owner}/{name}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rule.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
