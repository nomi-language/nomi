# Flatten Array

Subject: Collections & Algorithms

Flatten an arbitrarily nested array of integers into one flat list, leaving out nulls.

Adapted from Exercism problem-specifications (MIT), exercises/flatten-array.

## Choices

- The input is a recursive enum, `Nested`: a `Value Int`, an `Array List<Nested>`, or `Null`. The canonical arrays mix numbers, arrays and nulls, and the enum names each kind, so `flatten` handles exactly those three and the compiler checks it does.
- `flatten` is `Iter.flat_map` over the top level with `values`, which answers one element's values: `[n]` for a value, the flattened array for an array, and `[]` for a null. The recursion is between the two functions, one call per nested array.
- The canonical arrays are written as `Nested` values in the tests, `[.Value(1), .Array([.Value(2)]), .Null]`. std/json's `Json` could hold them, but it also holds strings, floats and objects, which this input never has, and there is no `Json` typed literal to write them as JSON.
- The table is annotated `List<#(String, List<Nested>, List<Int>)>`, since its first row's arrays are empty and give the element types nothing; with the annotation every variant is bare.
- The 11 current canonical cases are one `subtests` table named by the description column. The four cases that newer ones reimplement (the same arrays, described as lists instead of arrays) are left out.

## Friction

None.
