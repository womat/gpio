package rpi

import (
	"math"
	"testing"
)

func TestSeqGap(t *testing.T) {
	tests := []struct {
		name      string
		last, cur uint32
		want      uint64
	}{
		{"first edge of a watch", 0, 7, 0},
		{"kernel without sequence numbers", 0, 0, 0},
		{"consecutive", 7, 8, 0},
		{"one lost", 7, 9, 1},
		{"many lost", 100, 1100, 999},
		{"counter wrapped, nothing lost", math.MaxUint32 - 1, math.MaxUint32, 0},
		{"counter wrapped across a lost edge", math.MaxUint32, 1, 1},
		{"counter wrapped onto zero", math.MaxUint32, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := seqGap(tt.last, tt.cur); got != tt.want {
				t.Errorf("seqGap(%d, %d) = %d, want %d", tt.last, tt.cur, got, tt.want)
			}
		})
	}
}
