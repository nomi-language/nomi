# Flower Field

Subject: Grids & Matrices

Write the count of adjacent flowers into each empty square of a garden.

Adapted from Exercism problem-specifications (MIT), exercises/flower-field.

## Choices

- The garden stays `List<String>` in and out. Flowers are collected once into a `Set<#(Int, Int)>` of coordinates, so counting neighbours is eight set lookups and there is no grid indexing or bounds check.
- Rows are iterated as strings of graphemes (`Iter` over `String`), comparing squares with `"*"`, not as codepoints; style §19 keeps text in strings.
- The eight offsets are a `once` literal rather than generated from `-1..=1` twice with the centre removed.
- Each canonical case is its own `test`, with the garden bound to `garden` before the assertion.
- Every garden of more than one row, input and expected output, is written one row per line. `nomi fmt` keeps a list broken after its `[` that way even when it would fit on one line.

## Friction

None.
