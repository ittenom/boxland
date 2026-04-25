package persistence

import "testing"

func TestNormalizeMigrateURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"postgres://u:p@h:5432/db?sslmode=disable", "pgx5://u:p@h:5432/db?sslmode=disable"},
		{"postgresql://u@h/db", "pgx5://u@h/db"},
		{"pgx5://already/normalized", "pgx5://already/normalized"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeMigrateURL(c.in); got != c.want {
			t.Errorf("normalizeMigrateURL(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}
