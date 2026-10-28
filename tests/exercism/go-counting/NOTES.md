# Go Counting

Subject: Grids & Matrices

Count the territory on a Go board: the empty points that only one player's stones enclose.

Adapted from Exercism problem-specifications (MIT), exercises/go-counting.

## Choices

- The board is read once into a `Map<Point, String>` from each point, `#(x, y)`, to what stands on it. A point off the board has no key, so the bounds check in `territory` is one `Map.get` answering `None`, and the flood fill only ever asks the map.
- `territory` answers a `Territory`, a struct of its `owner` and its `points` as a `Set<Point>`, since the canonical territories are unordered lists. The owner is an `Owner` enum, `Black`, `White` and `Neither`, where the canonical data writes `"NONE"`; `None` is `Maybe`'s variant, so the third owner has another name.
- `fill` grows a territory from one empty point with `spread`, a recursion over a frontier list that collects the area and the stones it touches. The owner is the colour of those stones when there is exactly one, which is a `case` over the set as a list: `["B"]`, `["W"]`, or anything else.
- `territories` folds over the empty points with `Iter.reduce`, filling each point no territory has claimed yet and adding the territory to its owner's set.
- The one error, "Invalid coordinate", is a `String`, as in grains: an enum of one variant would add a type and say nothing more.
- The canonical cases are two `subtests` tables, one per property. The five-by-five board all `territory` rows share is a `once`.

## Friction

None.
