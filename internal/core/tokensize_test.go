package core

// token 估算的同口径回归：EstimateTokens 改成只吃长度后，两条入口必须完全一致。

import "testing"

func TestEstimateTokensForSizeMatchesPayloadForm(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 4, 5, 6, 100, 4095, 65536, 1048576} {
		want := EstimateTokens(make([]byte, size))
		got := EstimateTokensForSize(size)
		if got != want {
			t.Fatalf("size %d: EstimateTokensForSize = %d, EstimateTokens = %d", size, got, want)
		}
	}
}

func TestEstimateTokensForSizeEdgeCases(t *testing.T) {
	if got := EstimateTokensForSize(0); got != 0 {
		t.Fatalf("empty must be 0, got %d", got)
	}
	if got := EstimateTokensForSize(-1); got != 0 {
		t.Fatalf("negative must be 0, got %d", got)
	}
	if got := EstimateTokensForSize(1); got != 1 {
		t.Fatalf("tiny payload must floor at 1, got %d", got)
	}
}
