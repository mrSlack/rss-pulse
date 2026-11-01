package main

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultPoolMaxConns = 12
	minPoolMaxConns     = 2
	maxPoolMaxConns     = 100
)

// poolMaxConnsFromEnv reads DB_MAX_CONNS, the maximum size of the PostgreSQL connection
// pool. Unset uses the default; invalid values return an error so startup fails fast.
func poolMaxConnsFromEnv(getenv func(string) string) (int32, error) {
	raw := strings.TrimSpace(getenv("DB_MAX_CONNS"))
	if raw == "" {
		return defaultPoolMaxConns, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minPoolMaxConns || value > maxPoolMaxConns {
		return 0, fmt.Errorf("DB_MAX_CONNS must be a whole number from %d to %d", minPoolMaxConns, maxPoolMaxConns)
	}
	return int32(value), nil
}
