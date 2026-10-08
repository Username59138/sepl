package interp

import (
	"io"
	"os"
	"runtime/debug"
	"testing"
)

func benchFile(b *testing.B, path string) {
	defer debug.SetGCPercent(debug.SetGCPercent(400))
	src, err := os.ReadFile(path)
	if err != nil {
		b.Skip(err)
	}
	for i := 0; i < b.N; i++ {
		s := NewSession(io.Discard, nil)
		if _, err := s.RunFile(path, string(src), nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFib(b *testing.B)   { benchFile(b, "../bench/fib.sepl") }
func BenchmarkLoop(b *testing.B)  { benchFile(b, "../bench/loop.sepl") }
func BenchmarkLists(b *testing.B) { benchFile(b, "../bench/lists.sepl") }

func readExample(t *testing.T, name string) string {
	src, err := os.ReadFile("../examples/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
