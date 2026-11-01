package main

import "testing"

func TestPoolMaxConnsFromEnv(t *testing.T) {
	valid := map[string]int32{"": 12, "  ": 12, "2": 2, "12": 12, " 40 ": 40, "100": 100}
	for raw, want := range valid {
		got, err := poolMaxConnsFromEnv(func(key string) string {
			if key != "DB_MAX_CONNS" {
				t.Fatalf("read unexpected variable %q", key)
			}
			return raw
		})
		if err != nil || got != want {
			t.Errorf("DB_MAX_CONNS=%q: got (%d, %v), want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "1", "101", "-5", "ten", "12.5", "1e2"} {
		if got, err := poolMaxConnsFromEnv(func(string) string { return raw }); err == nil {
			t.Errorf("DB_MAX_CONNS=%q: got %d, want an error", raw, got)
		}
	}
}
