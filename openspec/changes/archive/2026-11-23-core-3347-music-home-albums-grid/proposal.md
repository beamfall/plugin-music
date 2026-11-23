## Why

CORE-3347. The Music Library's Recommended view ended with a `Library` grid of every Track
(`all-items`). Tracks make no sense on a browse home built from albums and artists, and the grid
showed duplicate cards when a library held two copies of a song. Film/TV dropped its catch-all grid
the same way (CORE-3066).

## What Changes

- The home layout drops the `all-items` `Library` grid. Every Track stays reachable through Core's
  Library tab.
- The `Albums` section moves below the `Artists` shelf and becomes a grid, so the home shows many
  albums. It is the only home section that reads the album `entity-items` Source, so no album is
  listed twice.

## Impact

- Core renders the layout as data, so web, Apple and Android pick the change up from the core
  repin of `github.com/beamfall/plugin-music`.
