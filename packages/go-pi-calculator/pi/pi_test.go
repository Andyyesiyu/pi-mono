package pi

import (
	"math"
	"testing"
)

func TestLeibniz(t *testing.T) {
	result := Leibniz(1000000)
	diff := math.Abs(result - math.Pi)
	if diff > 1e-5 {
		t.Errorf("Leibniz(1000000) = %f, diff from pi = %e, want < 1e-5", result, diff)
	}
}

func TestNilakantha(t *testing.T) {
	result := Nilakantha(100)
	diff := math.Abs(result - math.Pi)
	if diff > 1e-6 {
		t.Errorf("Nilakantha(100) = %f, diff from pi = %e, want < 1e-6", result, diff)
	}
}

func TestChudnovsky15(t *testing.T) {
	result := Chudnovsky(15)
	str := result.Text('f', 15)
	expected := "3.141592653589793"
	if str != expected {
		t.Errorf("Chudnovsky(15) = %s, want %s", str, expected)
	}
}

func TestChudnovsky50(t *testing.T) {
	result := Chudnovsky(50)
	str := result.Text('f', 50)
	expected := "3.14159265358979323846264338327950288419716939937511"
	if str != expected {
		t.Errorf("Chudnovsky(50) = %s, want %s", str, expected)
	}
}

func TestMonteCarlo(t *testing.T) {
	result := MonteCarlo(10000000)
	diff := math.Abs(result - math.Pi)
	if diff > 0.01 {
		t.Errorf("MonteCarlo(10000000) = %f, diff from pi = %e, want < 0.01", result, diff)
	}
}

func TestBBP(t *testing.T) {
	// The hex expansion of pi after "3." is: 243F6A8885A308D3...
	// Position 0 -> 2
	result := BBP(0)
	if result != 2 {
		t.Errorf("BBP(0) = %d, want 2", result)
	}
}

func TestLeibnizConvergence(t *testing.T) {
	r1 := Leibniz(100)
	r2 := Leibniz(10000)

	diff1 := math.Abs(r1 - math.Pi)
	diff2 := math.Abs(r2 - math.Pi)

	if diff2 >= diff1 {
		t.Errorf("Leibniz should converge: 100 iters diff=%e, 10000 iters diff=%e", diff1, diff2)
	}
}

func TestNilakanthaConvergence(t *testing.T) {
	r1 := Nilakantha(10)
	r2 := Nilakantha(100)

	diff1 := math.Abs(r1 - math.Pi)
	diff2 := math.Abs(r2 - math.Pi)

	if diff2 >= diff1 {
		t.Errorf("Nilakantha should converge: 10 iters diff=%e, 100 iters diff=%e", diff1, diff2)
	}
}

func BenchmarkLeibniz(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Leibniz(10000)
	}
}

func BenchmarkNilakantha(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Nilakantha(100)
	}
}

func BenchmarkChudnovsky(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Chudnovsky(50)
	}
}

func BenchmarkMonteCarlo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		MonteCarlo(100000)
	}
}
