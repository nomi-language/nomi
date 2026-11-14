# Benchmark results

Written by `benchmarks/run`; do not edit by hand. Each time is the median wall time of 5 runs after 1 warm-up run(s), in seconds. `nomi run` includes loading the program, from the artifact cache after the warm-up; the built executable is the program `nomi build` wrote. Go is the Benchmarks Game's Go program at the same size, run with `GOMAXPROCS=1` (the Nomi programs run on one task). The ratios are Nomi's time over Go's. Sizes are fixed so a Nomi run takes seconds; they are far below the Benchmarks Game's published sizes, so the Go times include more of Go's process start than at full size. How each program is written, and where the time goes, are in benchmarks/README.md and benchmarks/FINDINGS.md.

- Commit: `6561310c1`
- Machine: darwin/arm64, Apple M2 Max, 12 CPUs
- Go: `go version go1.27.0 darwin/arm64`
- Load average (1 min): 9.15 at the start, 9.71 at the end
- Date: 2026-11-13

## Benchmarks Game

| Benchmark | Size | `nomi run` | Built | Go | Run / Go | Built / Go |
|---|---|---:|---:|---:|---:|---:|
| binary-trees | 16 | 4.941 | 4.741 | 0.367 | 13× | 13× |
| fannkuch-redux | 9 | 3.657 | 3.821 | 0.029 | 125× | 131× |
| fasta | 250000 | 2.380 | 2.276 | 0.041 | 58× | 56× |
| k-nucleotide | fasta 25000 | 2.293 | 2.867 | 0.031 | 74× | 93× |
| mandelbrot | 1000 | 2.716 | 2.772 | 0.014 | 201× | 205× |
| n-body | 200000 | 2.358 | 3.170 | 0.018 | 132× | 177× |
| pidigits | 10000 | 2.313 | 2.194 | 1.061 | 2× | 2× |
| regex-redux | fasta 500000 | 2.311 | 2.365 | 1.174 | 2× | 2× |
| reverse-complement | fasta 1000000 | 4.021 | 3.448 | 0.012 | 324× | 278× |
| spectral-norm | 400 | 2.556 | 1.756 | 0.027 | 94× | 64× |

## Are We Fast Yet

| Benchmark | Iterations, inner | `nomi run` | Built |
|---|---|---:|---:|
| Bounce | 1, 200 | 0.943 | 0.886 |
| List | 1, 300 | 1.763 | 2.158 |
| Mandelbrot | 1, 750 | 1.812 | 1.773 |
| NBody | 1, 250000 | 3.919 | 3.552 |
| Permute | 1, 200 | 3.302 | 3.019 |
| Queens | 1, 100 | 0.966 | 0.902 |
| Sieve | 1, 10 | 0.109 | 0.087 |
| Storage | 1, 100 | 2.022 | 1.917 |
| Towers | 1, 100 | 1.083 | 1.020 |
| CD | 1, 250 | 6.549 | 7.879 |
| DeltaBlue | 1, 3000 | 2.195 | 2.039 |
| Havlak | 1, 1 | 7.687 | 7.870 |
| Json | 1, 50 | 2.076 | 2.111 |
| Richards | 1, 10 | 2.473 | 2.447 |

