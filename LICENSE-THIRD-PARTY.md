# Third-party attributions

Boxland is MIT-licensed (see `LICENSE`). This file lists open-source
algorithms and code patterns we have adapted from other MIT-licensed
projects. None of these are pulled in as runtime dependencies; the
attributions cover *adapted code* (rewrites in our own types and
idioms that are nonetheless inspired by the source).

## Wave Function Collapse — overlapping model

`server/internal/maps/wfc/overlapping.go` implements Maxim Gumin's
overlapping-model Wave Function Collapse algorithm, adapted to Go and
to our entity-type tile vocabulary.

- **Source:** https://github.com/mxgmn/WaveFunctionCollapse
- **Author:** Maxim Gumin (mxgmn) and contributors
- **License:** MIT
- **Adapted from:** `OverlappingModel.cs` and supporting files in the
  reference implementation. Boxland's port preserves the algorithmic
  structure (NxN pattern extraction, weighted observation, AC-style
  propagation) but rewrites the data flow against our `wfcCell`/
  `Region` types so it shares the propagation idioms in `generate.go`
  (the socket engine).

## Wave Function Collapse — Go propagator structure

`server/internal/maps/wfc/overlapping.go`'s propagation loop borrows
the iterative-frontier structure from Shawn Ridgeway's Go port of WFC.

- **Source:** https://github.com/shawnridgeway/wfc
- **Author:** Shawn Ridgeway
- **License:** MIT
- **Adapted from:** the propagator pattern in the simple-tiled and
  overlapping models. We do not import the package (its `image.Image`-
  oriented API doesn't match our entity-id model); the borrowing is at
  the level of "this is how to do AC propagation cleanly in Go."

## Wave Function Collapse — non-local constraints

`server/internal/maps/wfc/constraints.go` implements `BorderConstraint`
and `PathConstraint`, modelled on Boris the Brave's DeBroglie. Boxland
exposes a narrower init-only / verify-after API (no mid-search Ban /
Select hooks) — the algorithmic shape and the constraint catalogue
itself are the borrowed pieces.

- **Source:** https://github.com/BorisTheBrave/DeBroglie
- **Author:** Boris the Brave
- **License:** MIT
- **Adapted from:** the `ITileConstraint` interface plus
  `BorderConstraint` and the path-constraints article. Boxland's
  versions are clean-room rewrites against our `wfcCell` / `Region`
  types.

## Hierarchical WFC — biome pre-pass

`server/internal/maps/wfc/biome.go` implements a coarse value-noise
pre-pass that assigns each chunk in chunked WFC to one of N biomes,
then filters that chunk's tileset to biome-tagged tiles. The technique
is from the hierarchical-WFC research line (no single canonical
implementation; closest reference is fileho/Hierarchical-Wave-Function-
Collapse, MIT). The value-noise core is original.

- **Reference:** https://github.com/fileho/Hierarchical-Wave-Function-Collapse
- **License:** MIT (technique inspiration only — no code reuse)
