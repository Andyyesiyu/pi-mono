# go-pi-calculator

A Go program that calculates the mathematical constant pi using multiple algorithms.

## Algorithms

| Algorithm | Description | Convergence |
|-----------|-------------|-------------|
| **Chudnovsky** | Chudnovsky series with arbitrary precision via `math/big` | ~14 digits/iteration |
| **Leibniz** | Gregory-Leibniz series: `pi/4 = 1 - 1/3 + 1/5 - ...` | ~1 digit per 10x iterations |
| **Nilakantha** | `pi = 3 + 4/(2*3*4) - 4/(4*5*6) + ...` | Faster than Leibniz |
| **Monte Carlo** | Random sampling with deterministic LCG | Statistical estimate |
| **BBP** | Bailey-Borwein-Plouffe formula for individual hex digits | Exact hex digit extraction |

## Usage

```bash
# Default: Chudnovsky with 50 digits
go run .

# Chudnovsky with 100 digits
go run . -algorithm chudnovsky -digits 100

# Leibniz with 1M iterations
go run . -algorithm leibniz -iterations 1000000

# Nilakantha with 200 iterations
go run . -algorithm nilakantha -iterations 200

# Monte Carlo with 10M samples
go run . -algorithm montecarlo -iterations 10000000

# BBP: compute the 5th hex digit of pi
go run . -algorithm bbp -n 5
```

## Testing

```bash
go test -v ./pi/...
```

## Benchmarks

```bash
go test -bench=. ./pi/...
```
