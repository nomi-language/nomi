# Exercism exercises

Exercises from Exercism's problem specifications, solved in Nomi. Each
directory holds a solution, a test file with every canonical case, and a
`NOTES.md` that records the solution's judgement calls and the friction met
while writing it. `nomi test tests` runs them with the rest of the corpus.

The tour's Examples section is generated from these directories: each page
shows the files, the first paragraph of NOTES.md, its Choices, and its
Friction items as "Rough edges", so a reader sees where Nomi is still rough.
Friction lists only what is still true: when a change fixes an item, it
deletes the item (and marks the exercise `clean` below if none is left), and
the generator refuses an item that says `Fixed:`. The line
under NOTES.md's title names the exercise's subject, which groups it in the
tour's sidebar and filters the intro's table:

```
# Acronym

Subject: Strings & Text
```

The subject is one of a fixed set, in sidebar order: Strings & Text,
Numbers & Math, Collections & Algorithms, Parsing & Validation, Encoding &
Ciphers, Grids & Matrices, Games & Simulation, Dates & Time
(`subjectOrder` in `cmd/nomi-docgen/examples.go`). The generator refuses a
NOTES.md with no subject or another one, naming the file. After
editing an exercise, run `go run ./cmd/nomi-docgen -examples tests/exercism
tour/src/content/docs/examples` and regenerate tour.expect.

The problem descriptions and canonical data come from
[exercism/problem-specifications](https://github.com/exercism/problem-specifications),
MIT licence, Copyright (c) 2014, 2019, 2021 Exercism.

- [acronym](acronym/NOTES.md): clean
- [affine-cipher](affine-cipher/NOTES.md): friction
- [all-your-base](all-your-base/NOTES.md): clean
- [allergies](allergies/NOTES.md): clean
- [anagram](anagram/NOTES.md): clean
- [armstrong-numbers](armstrong-numbers/NOTES.md): friction
- [atbash-cipher](atbash-cipher/NOTES.md): friction
- [binary-search](binary-search/NOTES.md): clean
- [bob](bob/NOTES.md): clean
- [clock](clock/NOTES.md): clean
- [collatz-conjecture](collatz-conjecture/NOTES.md): clean
- [darts](darts/NOTES.md): clean
- [diamond](diamond/NOTES.md): clean
- [difference-of-squares](difference-of-squares/NOTES.md): clean
- [eliuds-eggs](eliuds-eggs/NOTES.md): clean
- [etl](etl/NOTES.md): clean
- [game-of-life](game-of-life/NOTES.md): clean
- [grains](grains/NOTES.md): friction
- [hamming](hamming/NOTES.md): clean
- [house](house/NOTES.md): clean
- [isbn-verifier](isbn-verifier/NOTES.md): clean
- [isogram](isogram/NOTES.md): clean
- [kindergarten-garden](kindergarten-garden/NOTES.md): friction
- [largest-series-product](largest-series-product/NOTES.md): clean
- [leap](leap/NOTES.md): clean
- [list-ops](list-ops/NOTES.md): clean
- [luhn](luhn/NOTES.md): clean
- [matching-brackets](matching-brackets/NOTES.md): clean
- [minesweeper](minesweeper/NOTES.md): clean
- [nth-prime](nth-prime/NOTES.md): clean
- [nucleotide-count](nucleotide-count/NOTES.md): clean
- [pangram](pangram/NOTES.md): clean
- [pascals-triangle](pascals-triangle/NOTES.md): clean
- [perfect-numbers](perfect-numbers/NOTES.md): clean
- [phone-number](phone-number/NOTES.md): clean
- [pig-latin](pig-latin/NOTES.md): clean
- [prime-factors](prime-factors/NOTES.md): clean
- [protein-translation](protein-translation/NOTES.md): clean
- [proverb](proverb/NOTES.md): clean
- [queen-attack](queen-attack/NOTES.md): clean
- [raindrops](raindrops/NOTES.md): clean
- [resistor-color](resistor-color/NOTES.md): clean
- [resistor-color-duo](resistor-color-duo/NOTES.md): clean
- [resistor-color-trio](resistor-color-trio/NOTES.md): clean
- [reverse-string](reverse-string/NOTES.md): clean
- [rna-transcription](rna-transcription/NOTES.md): friction
- [robot-simulator](robot-simulator/NOTES.md): clean
- [roman-numerals](roman-numerals/NOTES.md): clean
- [rotational-cipher](rotational-cipher/NOTES.md): clean
- [run-length-encoding](run-length-encoding/NOTES.md): friction
- [saddle-points](saddle-points/NOTES.md): friction
- [scrabble-score](scrabble-score/NOTES.md): clean
- [secret-handshake](secret-handshake/NOTES.md): clean
- [series](series/NOTES.md): clean
- [sieve](sieve/NOTES.md): clean
- [space-age](space-age/NOTES.md): clean
- [spiral-matrix](spiral-matrix/NOTES.md): clean
- [sublist](sublist/NOTES.md): clean
- [sum-of-multiples](sum-of-multiples/NOTES.md): clean
- [tournament](tournament/NOTES.md): clean
- [transpose](transpose/NOTES.md): clean
- [triangle](triangle/NOTES.md): clean
- [twelve-days](twelve-days/NOTES.md): clean
- [two-fer](two-fer/NOTES.md): clean
- [word-count](word-count/NOTES.md): clean
- [wordy](wordy/NOTES.md): friction
