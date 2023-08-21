package qids

import (
	"testing"

	"github.com/hugelgupf/p9/p9"
)

func TestGen(t *testing.T) {
	g := &PathGenerator{}
	m1 := NewMapper(g)
	m2 := NewMapper(g)

	for _, tt := range []struct {
		m    *Mapper
		q    p9.QID
		want p9.QID
	}{
		{m: m1, q: p9.QID{Type: p9.TypeDir, Version: 0, Path: 0}, want: p9.QID{Type: p9.TypeDir, Version: 0, Path: 1}},
		{m: m1, q: p9.QID{Type: p9.TypeDir, Version: 0, Path: 0}, want: p9.QID{Type: p9.TypeDir, Version: 0, Path: 1}},
		{m: m1, q: p9.QID{Type: p9.TypeDir, Version: 5, Path: 0}, want: p9.QID{Type: p9.TypeDir, Version: 5, Path: 1}},
		{m: m2, q: p9.QID{Type: p9.TypeDir, Version: 0, Path: 0}, want: p9.QID{Type: p9.TypeDir, Version: 0, Path: 2}},
		{m: m2, q: p9.QID{Type: p9.TypeDir, Version: 0, Path: 0}, want: p9.QID{Type: p9.TypeDir, Version: 0, Path: 2}},
		{m: m1, q: p9.QID{Type: p9.TypeDir, Version: 1, Path: 1}, want: p9.QID{Type: p9.TypeDir, Version: 1, Path: 3}},
		{m: m2, q: p9.QID{Type: p9.TypeDir, Version: 1, Path: 1}, want: p9.QID{Type: p9.TypeDir, Version: 1, Path: 4}},
	} {
		if got := tt.m.QIDFor(tt.q); got != tt.want {
			t.Errorf("QIDFor(%v) = %v, want %v", tt.q, got, tt.want)
		}
	}
}
