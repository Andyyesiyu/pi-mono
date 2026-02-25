// Package pi provides functions to calculate the mathematical constant pi
// using various algorithms.
package pi

import (
	"math"
	"math/big"
)

// Leibniz calculates pi using the Leibniz formula (Gregory-Leibniz series):
// pi/4 = 1 - 1/3 + 1/5 - 1/7 + ...
// Convergence is slow: roughly one correct digit per order of magnitude of iterations.
func Leibniz(iterations int) float64 {
	sum := 0.0
	for k := 0; k < iterations; k++ {
		term := 1.0 / float64(2*k+1)
		if k%2 == 0 {
			sum += term
		} else {
			sum -= term
		}
	}
	return sum * 4
}

// Nilakantha calculates pi using the Nilakantha series:
// pi = 3 + 4/(2*3*4) - 4/(4*5*6) + 4/(6*7*8) - ...
// Converges faster than Leibniz.
func Nilakantha(iterations int) float64 {
	sum := 3.0
	for k := 1; k <= iterations; k++ {
		n := float64(2 * k)
		term := 4.0 / (n * (n + 1) * (n + 2))
		if k%2 == 1 {
			sum += term
		} else {
			sum -= term
		}
	}
	return sum
}

// factorial computes n! as a big.Int.
func factorial(n int64) *big.Int {
	result := big.NewInt(1)
	for i := int64(2); i <= n; i++ {
		result.Mul(result, big.NewInt(i))
	}
	return result
}

// Chudnovsky calculates pi using the Chudnovsky algorithm.
// This is one of the fastest known algorithms for computing pi,
// converging at about 14 digits per iteration.
// The digits parameter specifies how many decimal digits of precision to compute.
func Chudnovsky(digits int) *big.Float {
	if digits < 1 {
		digits = 1
	}

	prec := uint(float64(digits)*3.4) + 128
	iterations := digits/14 + 2

	sum := new(big.Float).SetPrec(prec).SetFloat64(0)
	termFlt := new(big.Float).SetPrec(prec)

	for k := 0; k < iterations; k++ {
		kk := int64(k)

		// numerator = (-1)^k * (6k)! * (13591409 + 545140134*k)
		num := factorial(6 * kk)
		linear := new(big.Int).SetInt64(545140134)
		linear.Mul(linear, big.NewInt(kk))
		linear.Add(linear, big.NewInt(13591409))
		num.Mul(num, linear)
		if k%2 == 1 {
			num.Neg(num)
		}

		// denominator = (3k)! * (k!)^3 * (640320)^(3k+3/2)
		// We handle the 640320^(3k) part as integer and sqrt(640320^3) separately
		den := factorial(3 * kk)
		kFact := factorial(kk)
		kFact3 := new(big.Int).Mul(kFact, kFact)
		kFact3.Mul(kFact3, kFact)
		den.Mul(den, kFact3)

		// 640320^(3k)
		c := big.NewInt(640320)
		c3k := new(big.Int).SetInt64(1)
		for i := int64(0); i < 3*kk; i++ {
			c3k.Mul(c3k, c)
		}
		den.Mul(den, c3k)

		numFlt := new(big.Float).SetPrec(prec).SetInt(num)
		denFlt := new(big.Float).SetPrec(prec).SetInt(den)
		termFlt.Quo(numFlt, denFlt)

		sum.Add(sum, termFlt)
	}

	// Multiply sum by 12
	twelve := new(big.Float).SetPrec(prec).SetFloat64(12)
	sum.Mul(sum, twelve)

	// Compute sqrt(640320^3) = 640320 * sqrt(640320)
	c640320 := new(big.Float).SetPrec(prec).SetFloat64(640320)
	sqrtC := new(big.Float).SetPrec(prec).Sqrt(c640320)
	cThreeHalf := new(big.Float).SetPrec(prec).Mul(c640320, sqrtC)
	cThreeHalf.Mul(cThreeHalf, c640320) // 640320^(3/2) = 640320 * 640320^(1/2) ... wait

	// Actually: the Chudnovsky formula is:
	// 1/pi = 12 * sum_k [ (-1)^k (6k)! (A + Bk) / ((3k)!(k!)^3 * C^(3k+3/2)) ]
	// where A=13591409, B=545140134, C=640320
	// C^(3k+3/2) = C^(3k) * C^(3/2)
	// C^(3/2) = C * sqrt(C) = 640320 * sqrt(640320)

	// We already divided by C^(3k) in the loop. Now divide sum by C^(3/2).
	sqrtC640320 := new(big.Float).SetPrec(prec).SetFloat64(640320)
	sqrtC640320 = sqrtC640320.Sqrt(sqrtC640320)
	cThreeHalfVal := new(big.Float).SetPrec(prec).SetFloat64(640320)
	cThreeHalfVal.Mul(cThreeHalfVal, sqrtC640320)

	sum.Quo(sum, cThreeHalfVal)

	// sum now equals 1/pi, so pi = 1/sum
	one := new(big.Float).SetPrec(prec).SetFloat64(1)
	result := new(big.Float).SetPrec(prec)
	result.Quo(one, sum)

	return result
}

// MonteCarlo estimates pi using the Monte Carlo method.
// Random points are generated in a unit square and the ratio
// of points inside the unit circle estimates pi/4.
// Uses a deterministic seed for reproducibility.
func MonteCarlo(samples int) float64 {
	// Use a simple LCG for deterministic results
	seed := uint64(42)
	inside := 0

	for i := 0; i < samples; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		x := float64(seed>>33) / float64(1<<31)
		seed = seed*6364136223846793005 + 1442695040888963407
		y := float64(seed>>33) / float64(1<<31)

		if x*x+y*y <= 1.0 {
			inside++
		}
	}

	return 4.0 * float64(inside) / float64(samples)
}

// BBP calculates the nth hexadecimal digit of pi using the
// Bailey-Borwein-Plouffe formula. This can compute individual
// hex digits without computing all preceding digits.
// Returns the nth hex digit (0-indexed, starting after "3.") as an integer 0-15.
func BBP(n int) int {
	s1 := bbpSum(n, 1)
	s2 := bbpSum(n, 4)
	s3 := bbpSum(n, 5)
	s4 := bbpSum(n, 6)

	frac := 4*s1 - 2*s2 - s3 - s4
	frac = frac - math.Floor(frac)
	if frac < 0 {
		frac += 1
	}

	return int(frac * 16)
}

func bbpSum(n, j int) float64 {
	s := 0.0

	for k := 0; k <= n; k++ {
		ak := 8*k + j
		r := modPow(16, n-k, ak)
		s += float64(r) / float64(ak)
		s = s - math.Floor(s)
	}

	for k := n + 1; k < n+100; k++ {
		ak := 8*k + j
		term := pow16(n-k) / float64(ak)
		if term < 1e-17 {
			break
		}
		s += term
		s = s - math.Floor(s)
	}

	return s
}

// modPow computes (base^exp) mod m using binary exponentiation.
func modPow(base, exp, m int) int {
	if m == 1 {
		return 0
	}
	if exp < 0 {
		return 0
	}
	result := 1
	base = base % m
	for exp > 0 {
		if exp%2 == 1 {
			result = (result * base) % m
		}
		exp /= 2
		base = (base * base) % m
	}
	return result
}

// pow16 computes 16^n for negative n (fractional results).
func pow16(n int) float64 {
	if n >= 0 {
		result := 1.0
		for i := 0; i < n; i++ {
			result *= 16
		}
		return result
	}
	result := 1.0
	for i := 0; i > n; i-- {
		result /= 16
	}
	return result
}
