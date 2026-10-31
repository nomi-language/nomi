# POV

Subject: Collections & Algorithms

Reparent a tree on one of its nodes, so it is seen from that node's point of view, and find the path between two nodes.

Adapted from Exercism problem-specifications (MIT), exercises/pov.

## Choices

- `Tree` is a struct of a `String` label and a `List<Tree>` of children, which default to none, so a leaf is `#{label: "x"}` and the tests write each canonical tree as nested literals.
- `Tree.from_pov` is one recursion, `reroot`, that carries the old parent re-hung as a tree: at each node it tries every child, handing it the node without that child and with the node's own old parent added as a child. At the target the carried tree becomes the last child. Nothing is changed in place; each attempt builds its own nodes, and the attempts that do not reach the target are dropped.
- The carried parent is a `List<Tree>` of none or one rather than a `Maybe<Tree>`, so it joins the children with a spread, `[..tree.children, ..above]`, and needs no case at the old root.
- A missing label is `None`, from `from_pov` and `path_to` alike. The canonical data's `null` names no error to report, and both lookups are of data the caller passes in.
- `path_to` reroots at `from` and reads the labels from the new root down to `to` with `labels_to`, so the path through the old root comes for free. A missing `from` ends it at the reroot's `?`.
- Each recursion takes the first child that answers with `Iter.find_map`, which stops at that child.
- The canonical data leaves the order of children open (the cousins case lists `kid-1` before `kid-0`), so the tests compare both trees after `in_label_order`, which sorts every node's children by label.
- The trees the cases share are `once` values in the test file. The two canonical groups are two tests, each one `subtests` table named by the description column.

## Friction

None.
