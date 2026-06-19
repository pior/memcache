package main

import (
	"fmt"
	"testing"
)

func TestTrimmedMean(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		want    string
	}{
		{"empty", nil, "0.00"},
		{"single", []float64{42}, "42.00"},
		{"two averages both", []float64{10, 20}, "15.00"},
		{"three drops min and max", []float64{10, 100, 1000}, "100.00"},
		{"drops one fastest and one slowest", []float64{1, 10, 11, 12, 1000}, "11.00"},
		{"duplicate extremes trimmed once each", []float64{5, 5, 5, 5, 5}, "5.00"},
		{"outlier high damped", []float64{100, 102, 104, 106, 5000}, "104.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf("%.2f", trimmedMean(tt.samples))
			if got != tt.want {
				t.Errorf("trimmedMean(%v) = %s, want %s", tt.samples, got, tt.want)
			}
		})
	}
}

func TestStddev(t *testing.T) {
	tests := []struct {
		name    string
		samples []float64
		want    string
	}{
		{"empty", nil, "0.00"},
		{"single has no scatter", []float64{42}, "0.00"},
		{"identical samples", []float64{10, 10, 10}, "0.00"},
		{"known spread", []float64{2, 4, 4, 4, 5, 5, 7, 9}, "2.14"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmt.Sprintf("%.2f", stddev(tt.samples))
			if got != tt.want {
				t.Errorf("stddev(%v) = %s, want %s", tt.samples, got, tt.want)
			}
		})
	}
}
