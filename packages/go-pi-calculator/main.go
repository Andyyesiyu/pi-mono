package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/badlogic/pi-mono/packages/go-pi-calculator/pi"
)

func main() {
	algorithm := flag.String("algorithm", "chudnovsky", "Algorithm to use: leibniz, nilakantha, chudnovsky, montecarlo, bbp")
	iterations := flag.Int("iterations", 0, "Number of iterations (for leibniz, nilakantha, montecarlo)")
	digits := flag.Int("digits", 50, "Number of decimal digits (for chudnovsky)")
	bbpDigit := flag.Int("n", 0, "Hex digit position to compute (for bbp, 0-indexed)")
	flag.Parse()

	algo := strings.ToLower(*algorithm)

	start := time.Now()

	switch algo {
	case "leibniz":
		iters := *iterations
		if iters == 0 {
			iters = 1000000
		}
		result := pi.Leibniz(iters)
		elapsed := time.Since(start)
		fmt.Printf("Algorithm:  Leibniz series\n")
		fmt.Printf("Iterations: %d\n", iters)
		fmt.Printf("Pi =        %.15f\n", result)
		fmt.Printf("Time:       %v\n", elapsed)

	case "nilakantha":
		iters := *iterations
		if iters == 0 {
			iters = 100
		}
		result := pi.Nilakantha(iters)
		elapsed := time.Since(start)
		fmt.Printf("Algorithm:  Nilakantha series\n")
		fmt.Printf("Iterations: %d\n", iters)
		fmt.Printf("Pi =        %.15f\n", result)
		fmt.Printf("Time:       %v\n", elapsed)

	case "chudnovsky":
		d := *digits
		result := pi.Chudnovsky(d)
		elapsed := time.Since(start)
		fmt.Printf("Algorithm:  Chudnovsky\n")
		fmt.Printf("Digits:     %d\n", d)
		fmt.Printf("Pi =        %s\n", result.Text('f', d))
		fmt.Printf("Time:       %v\n", elapsed)

	case "montecarlo":
		samples := *iterations
		if samples == 0 {
			samples = 10000000
		}
		result := pi.MonteCarlo(samples)
		elapsed := time.Since(start)
		fmt.Printf("Algorithm:  Monte Carlo\n")
		fmt.Printf("Samples:    %d\n", samples)
		fmt.Printf("Pi ~        %.10f\n", result)
		fmt.Printf("Time:       %v\n", elapsed)

	case "bbp":
		n := *bbpDigit
		result := pi.BBP(n)
		elapsed := time.Since(start)
		fmt.Printf("Algorithm:  Bailey-Borwein-Plouffe\n")
		fmt.Printf("Position:   %d\n", n)
		fmt.Printf("Hex digit:  %X\n", result)
		fmt.Printf("Time:       %v\n", elapsed)

	default:
		fmt.Fprintf(os.Stderr, "Unknown algorithm: %s\n", *algorithm)
		fmt.Fprintf(os.Stderr, "Available: leibniz, nilakantha, chudnovsky, montecarlo, bbp\n")
		os.Exit(1)
	}
}
