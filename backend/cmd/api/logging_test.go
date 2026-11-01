package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func envFunc(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestNewLoggerDefaultsToTextAtInfo(t *testing.T) {
	var output bytes.Buffer
	logger, err := newLogger(envFunc(nil), &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("hidden")
	logger.Info("shown", "key", "value")
	if strings.Contains(output.String(), "hidden") {
		t.Fatalf("debug record written at default level: %s", output.String())
	}
	if !strings.Contains(output.String(), "level=INFO msg=shown key=value") {
		t.Fatalf("text output = %q", output.String())
	}
}

func TestNewLoggerJSONAndLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := newLogger(envFunc(map[string]string{"LOG_FORMAT": " JSON ", "LOG_LEVEL": "warn"}), &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hidden")
	logger.Warn("shown", "count", 3)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("output %q is not one JSON record: %v", output.String(), err)
	}
	if record["msg"] != "shown" || record["level"] != "WARN" || record["count"] != float64(3) {
		t.Fatalf("record = %v", record)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for raw, enabled := range map[string]string{"debug": "DEBUG", "INFO": "INFO", "warn": "WARN", "warning": "WARN", "error": "ERROR"} {
		var output bytes.Buffer
		logger, err := newLogger(envFunc(map[string]string{"LOG_LEVEL": raw}), &output)
		if err != nil {
			t.Fatalf("LOG_LEVEL=%q: %v", raw, err)
		}
		logger.Debug("d")
		logger.Info("i")
		logger.Warn("w")
		logger.Error("e")
		if !strings.Contains(output.String(), "level="+enabled+" ") {
			t.Fatalf("LOG_LEVEL=%q did not write %s records: %q", raw, enabled, output.String())
		}
	}
}

func TestNewLoggerRejectsInvalidValues(t *testing.T) {
	for _, env := range []map[string]string{
		{"LOG_FORMAT": "yaml"},
		{"LOG_LEVEL": "verbose"},
		{"LOG_LEVEL": "-4"},
	} {
		if _, err := newLogger(envFunc(env), &bytes.Buffer{}); err == nil {
			t.Fatalf("newLogger(%v) accepted an invalid value", env)
		}
	}
}
