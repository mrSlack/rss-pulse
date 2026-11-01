// Package metricstest reads single sample values from a Prometheus gatherer in tests.
package metricstest

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// Value returns the value of the counter, gauge or untyped sample of the family name
// whose labels equal labels exactly (histograms report their sample count). It fails
// the test when there is no such sample.
func Value(t testing.TB, gatherer prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	value, ok := Lookup(t, gatherer, name, labels)
	if !ok {
		t.Fatalf("no sample %s%v", name, labels)
	}
	return value
}

// Lookup is Value without failing when the sample does not exist.
func Lookup(t testing.TB, gatherer prometheus.Gatherer, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	families, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if len(metric.GetLabel()) != len(labels) {
				continue
			}
			matches := true
			for _, pair := range metric.GetLabel() {
				if want, ok := labels[pair.GetName()]; !ok || want != pair.GetValue() {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
			switch {
			case metric.Counter != nil:
				return metric.GetCounter().GetValue(), true
			case metric.Gauge != nil:
				return metric.GetGauge().GetValue(), true
			case metric.Histogram != nil:
				return float64(metric.GetHistogram().GetSampleCount()), true
			case metric.Untyped != nil:
				return metric.GetUntyped().GetValue(), true
			}
		}
	}
	return 0, false
}
