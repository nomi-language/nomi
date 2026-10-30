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
- [affine-cipher](affine-cipher/NOTES.md): clean
- [all-your-base](all-your-base/NOTES.md): clean
- [allergies](allergies/NOTES.md): clean
- [alphametics](alphametics/NOTES.md): clean
- [anagram](anagram/NOTES.md): clean
- [armstrong-numbers](armstrong-numbers/NOTES.md): friction
- [atbash-cipher](atbash-cipher/NOTES.md): clean
- [baffling-birthdays](baffling-birthdays/NOTES.md): clean
- [bank-account](bank-account/NOTES.md): clean
- [binary-search](binary-search/NOTES.md): clean
- [binary-search-tree](binary-search-tree/NOTES.md): clean
- [bob](bob/NOTES.md): clean
- [book-store](book-store/NOTES.md): clean
- [bottle-song](bottle-song/NOTES.md): clean
- [bowling](bowling/NOTES.md): clean
- [camicia](camicia/NOTES.md): clean
- [change](change/NOTES.md): clean
- [circular-buffer](circular-buffer/NOTES.md): clean
- [clock](clock/NOTES.md): clean
- [collatz-conjecture](collatz-conjecture/NOTES.md): clean
- [complex-numbers](complex-numbers/NOTES.md): clean
- [connect](connect/NOTES.md): clean
- [crypto-square](crypto-square/NOTES.md): clean
- [custom-set](custom-set/NOTES.md): clean
- [darts](darts/NOTES.md): clean
- [diamond](diamond/NOTES.md): clean
- [difference-of-squares](difference-of-squares/NOTES.md): clean
- [dnd-character](dnd-character/NOTES.md): clean
- [dominoes](dominoes/NOTES.md): clean
- [dot-dsl](dot-dsl/NOTES.md): clean
- [eliuds-eggs](eliuds-eggs/NOTES.md): clean
- [error-handling](error-handling/NOTES.md): clean
- [etl](etl/NOTES.md): clean
- [flatten-array](flatten-array/NOTES.md): clean
- [flower-field](flower-field/NOTES.md): clean
- [food-chain](food-chain/NOTES.md): clean
- [forth](forth/NOTES.md): clean
- [game-of-life](game-of-life/NOTES.md): clean
- [gigasecond](gigasecond/NOTES.md): clean
- [go-counting](go-counting/NOTES.md): clean
- [grade-school](grade-school/NOTES.md): clean
- [grains](grains/NOTES.md): friction
- [grep](grep/NOTES.md): friction
- [hamming](hamming/NOTES.md): clean
- [hello-world](hello-world/NOTES.md): clean
- [high-scores](high-scores/NOTES.md): clean
- [house](house/NOTES.md): clean
- [intergalactic-transmission](intergalactic-transmission/NOTES.md): friction
- [isbn-verifier](isbn-verifier/NOTES.md): clean
- [isogram](isogram/NOTES.md): clean
- [killer-sudoku-helper](killer-sudoku-helper/NOTES.md): clean
- [kindergarten-garden](kindergarten-garden/NOTES.md): clean
- [knapsack](knapsack/NOTES.md): clean
- [largest-series-product](largest-series-product/NOTES.md): clean
- [leap](leap/NOTES.md): clean
- [ledger](ledger/NOTES.md): clean
- [lens-person](lens-person/NOTES.md): clean
- [line-up](line-up/NOTES.md): clean
- [linked-list](linked-list/NOTES.md): clean
- [list-ops](list-ops/NOTES.md): clean
- [luhn](luhn/NOTES.md): clean
- [markdown](markdown/NOTES.md): friction
- [matching-brackets](matching-brackets/NOTES.md): clean
- [matrix](matrix/NOTES.md): clean
- [meetup](meetup/NOTES.md): clean
- [micro-blog](micro-blog/NOTES.md): clean
- [nth-prime](nth-prime/NOTES.md): clean
- [nucleotide-count](nucleotide-count/NOTES.md): clean
- [ocr-numbers](ocr-numbers/NOTES.md): clean
- [paasio](paasio/NOTES.md): friction
- [palindrome-products](palindrome-products/NOTES.md): clean
- [pangram](pangram/NOTES.md): clean
- [parallel-letter-frequency](parallel-letter-frequency/NOTES.md): friction
- [pascals-triangle](pascals-triangle/NOTES.md): clean
- [perfect-numbers](perfect-numbers/NOTES.md): clean
- [phone-number](phone-number/NOTES.md): clean
- [piecing-it-together](piecing-it-together/NOTES.md): clean
- [pig-latin](pig-latin/NOTES.md): clean
- [poker](poker/NOTES.md): friction
- [pov](pov/NOTES.md): clean
- [prime-factors](prime-factors/NOTES.md): clean
- [prism](prism/NOTES.md): clean
- [protein-translation](protein-translation/NOTES.md): clean
- [proverb](proverb/NOTES.md): clean
- [pythagorean-triplet](pythagorean-triplet/NOTES.md): clean
- [queen-attack](queen-attack/NOTES.md): clean
- [rail-fence-cipher](rail-fence-cipher/NOTES.md): clean
- [raindrops](raindrops/NOTES.md): clean
- [rational-numbers](rational-numbers/NOTES.md): clean
- [react](react/NOTES.md): clean
- [rectangles](rectangles/NOTES.md): clean
- [relative-distance](relative-distance/NOTES.md): clean
- [resistor-color](resistor-color/NOTES.md): clean
- [resistor-color-duo](resistor-color-duo/NOTES.md): clean
- [resistor-color-tolerance](resistor-color-tolerance/NOTES.md): clean
- [resistor-color-trio](resistor-color-trio/NOTES.md): clean
- [rest-api](rest-api/NOTES.md): clean
- [reverse-string](reverse-string/NOTES.md): clean
- [rna-transcription](rna-transcription/NOTES.md): clean
- [robot-name](robot-name/NOTES.md): clean
- [robot-simulator](robot-simulator/NOTES.md): clean
- [roman-numerals](roman-numerals/NOTES.md): clean
- [rotational-cipher](rotational-cipher/NOTES.md): clean
- [run-length-encoding](run-length-encoding/NOTES.md): friction
- [saddle-points](saddle-points/NOTES.md): clean
- [satellite](satellite/NOTES.md): clean
- [save-the-cow](save-the-cow/NOTES.md): clean
- [say](say/NOTES.md): clean
- [scrabble-score](scrabble-score/NOTES.md): clean
- [secret-handshake](secret-handshake/NOTES.md): clean
- [series](series/NOTES.md): clean
- [sgf-parsing](sgf-parsing/NOTES.md): friction
- [sieve](sieve/NOTES.md): clean
- [simple-cipher](simple-cipher/NOTES.md): clean
- [simple-linked-list](simple-linked-list/NOTES.md): clean
- [space-age](space-age/NOTES.md): clean
- [spiral-matrix](spiral-matrix/NOTES.md): clean
- [split-second-stopwatch](split-second-stopwatch/NOTES.md): clean
- [square-root](square-root/NOTES.md): clean
- [state-of-tic-tac-toe](state-of-tic-tac-toe/NOTES.md): clean
- [strain](strain/NOTES.md): clean
- [sublist](sublist/NOTES.md): clean
- [sum-of-multiples](sum-of-multiples/NOTES.md): clean
- [swift-scheduling](swift-scheduling/NOTES.md): clean
- [tournament](tournament/NOTES.md): clean
- [transpose](transpose/NOTES.md): clean
- [tree-building](tree-building/NOTES.md): clean
- [triangle](triangle/NOTES.md): clean
- [twelve-days](twelve-days/NOTES.md): clean
- [two-bucket](two-bucket/NOTES.md): clean
- [two-fer](two-fer/NOTES.md): clean
- [variable-length-quantity](variable-length-quantity/NOTES.md): friction
- [word-count](word-count/NOTES.md): clean
- [word-search](word-search/NOTES.md): clean
- [wordy](wordy/NOTES.md): clean
- [yacht](yacht/NOTES.md): clean
- [zebra-puzzle](zebra-puzzle/NOTES.md): clean
- [zipper](zipper/NOTES.md): friction

## Coverage

All 139 active exercises in problem-specifications are ported.

The 13 deprecated exercises are not ported, on purpose. Each replacement
is the one its `.deprecated` file names upstream:

| Exercise | Status upstream |
|---|---|
| accumulate | deprecated upstream; replaced by list-ops |
| beer-song | deprecated upstream; replaced by bottle-song |
| binary | deprecated upstream; replaced by all-your-base |
| counter | deprecated upstream |
| diffie-hellman | deprecated upstream |
| hangman | deprecated upstream; replaced by save-the-cow |
| hexadecimal | deprecated upstream; replaced by all-your-base |
| minesweeper | deprecated upstream; replaced by flower-field |
| nucleotide-codons | deprecated upstream; replaced by protein-translation |
| octal | deprecated upstream; replaced by all-your-base |
| point-mutations | deprecated upstream; replaced by hamming |
| scale-generator | deprecated upstream |
| trinary | deprecated upstream; replaced by all-your-base |

## Adapted exercises

These ports change the exercise's shape, usually because it asks for an
object that changes in place and Nomi values never change. Each NOTES.md
explains the change in its `Adapted:` item.

- [bank-account](bank-account/NOTES.md): the balance is the state of one task, the teller, and each operation is a request to it with its own reply channel.
- [dot-dsl](dot-dsl/NOTES.md): parses DOT text into a `Graph` value, as the canonical data does, rather than offering an internal DSL.
- [error-handling](error-handling/NOTES.md): no exceptions, so a missing answer is `None`, a handled failure an `Err`, a broken promise a trap, and a resource is released on success, failure or trap.
- [lens-person](lens-person/NOTES.md): plain reads and spread updates of nested fields, with no `Lens` type.
- [linked-list](linked-list/NOTES.md): a persistent double-ended list with the same operations and costs, since no node can point back at a later one.
- [paasio](paasio/NOTES.md): a meter is a value pairing the stream with its `Stats`, and each read or write answers the meter after it.
- [react](react/NOTES.md): the reactor is a value, and `set_value` answers the reactor after the change and the callbacks that fire, as a map from callback name to value.
- [robot-name](robot-name/NOTES.md): a `Factory` value holds the names given so far and the random seed, and building or resetting a robot answers the factory after it.
- [save-the-cow](save-the-cow/NOTES.md): the game is a value, `Game.guess` answers the game after the guess, and a sequence of guesses is a fold.
- [split-second-stopwatch](split-second-stopwatch/NOTES.md): the stopwatch is a value, each command answers the stopwatch after it, and time passes only through `Stopwatch.advance`.
