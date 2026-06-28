package unit

import (
	"testing"

	"tgsc/internal/domain"
	"tgsc/internal/sync"
)

func TestNormalizer_NormalizePrice(t *testing.T) {
	n := sync.NewNormalizer()
	tests := []struct {
		input    float64
		expected float64
	}{
		{19.999, 20.00},
		{10.001, 10.00},
		{10.00, 10.00},
		{9.999, 10.00},
	}
	for _, tt := range tests {
		result := n.NormalizePrice(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizePrice(%f) = %f, want %f", tt.input, result, tt.expected)
		}
	}
}

func TestNormalizer_NormalizeStock(t *testing.T) {
	n := sync.NewNormalizer()
	n.SetStockBuffer(5)
	tests := []struct {
		input    int
		expected int
	}{
		{10, 5},
		{5, 0},
		{3, 0},
		{0, 0},
		{-1, 0},
	}
	for _, tt := range tests {
		result := n.NormalizeStock(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeStock(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

func TestNormalizer_GenerateFingerprint(t *testing.T) {
	n := sync.NewNormalizer()
	p1 := &domain.Product{Title: "Product A", Price: 10.00, Stock: 5}
	p2 := &domain.Product{Title: "Product A", Price: 10.00, Stock: 5}
	p3 := &domain.Product{Title: "Product A", Price: 12.00, Stock: 5}
	p4 := &domain.Product{Title: "Product B", Price: 10.00, Stock: 5}

	fp1 := n.GenerateFingerprint(p1)
	fp2 := n.GenerateFingerprint(p2)
	fp3 := n.GenerateFingerprint(p3)
	fp4 := n.GenerateFingerprint(p4)

	if fp1 != fp2 {
		t.Error("Fingerprints for identical products differ")
	}
	if fp1 == fp3 {
		t.Error("Fingerprints for different prices are equal")
	}
	if fp1 == fp4 {
		t.Error("Fingerprints for different titles are equal")
	}
}