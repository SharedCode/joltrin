package memory

import (
	"math"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestCosineSimilarity(t *testing.T) {
	v1 := []float32{1, 0, 0}
	v2 := []float32{0, 1, 0}

	// orthogonal
	if CosineSimilarity(v1, v2) != 0 {
		t.Errorf("expected 0")
	}

	// identity
	if CosineSimilarity(v1, v1) != 1 {
		t.Errorf("expected 1")
	}

	// shared dimensions should be compared when lengths differ
	if CosineSimilarity(v1, []float32{1}) != 1 {
		t.Errorf("expected 1 over the shared dimension")
	}
	// zeros
	if CosineSimilarity([]float32{0, 0, 0}, v1) != 0 {
		t.Errorf("expected 0")
	}
}

func TestNormalizeVectorReturnsUnitNorm(t *testing.T) {
	got := NormalizeVector([]float32{3, 4})
	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %d", len(got))
	}

	norm := 0.0
	for _, val := range got {
		norm += float64(val * val)
	}
	if math.Abs(norm-1) > 1e-6 {
		t.Fatalf("expected unit norm, got %v", norm)
	}
}

func TestDistanceHandlesMismatchedVectorLengths(t *testing.T) {
	dist := Distance([]float32{1, 0, 0}, []float32{0, 1}, true)
	if dist != 1.4142135 {
		t.Fatalf("expected normalized distance over the shared dimensions, got %v", dist)
	}

	euclid := EuclideanDistance([]float32{1, 1, 1}, []float32{1, 2})
	if euclid != 1 {
		t.Fatalf("expected Euclidean distance over the shared dimensions, got %v", euclid)
	}
}

func TestFindClosestCategories(t *testing.T) {
	categories := []*Category{
		{ID: sop.NewUUID(), CenterVector: []float32{1, 0, 0}},
		{ID: sop.NewUUID(), CenterVector: []float32{0, 1, 0}},
		{ID: sop.NewUUID(), CenterVector: []float32{0, 0, 1}},
	}

	c := FindClosestCategories([]float32{1, 0, 0}, categories, 2)
	if len(c) != 2 {
		t.Errorf("expected 2 elements")
	}

	cZero := FindClosestCategories([]float32{1, 0, 0}, []*Category{}, 2)
	if len(cZero) != 0 {
		t.Errorf("expected 0 elements")
	}
}
