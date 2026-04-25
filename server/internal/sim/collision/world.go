package collision

// MapWorld is a sparse in-memory World backed by a Go map keyed by
// (gx, gy). Used by tests + by simple production scenarios where the
// game's tile authority is the same process. The live tick loop will
// likely back its own World by spatial.Grid + the Tile component store.
type MapWorld struct {
	tiles map[uint64]Tile
}

// BuildWorld constructs a MapWorld from a flat tile slice.
func BuildWorld(tiles []Tile) *MapWorld {
	w := &MapWorld{tiles: make(map[uint64]Tile, len(tiles))}
	for _, t := range tiles {
		w.tiles[mapKey(t.GX, t.GY)] = t
	}
	return w
}

// TileAt returns the tile at (gx, gy), if any.
func (w *MapWorld) TileAt(gx, gy int32) (Tile, bool) {
	t, ok := w.tiles[mapKey(gx, gy)]
	return t, ok
}

// mapKey packs (gx, gy) into a uint64 the same way spatial.MakeChunkID
// does, so this package and spatial.Grid can share key conventions if
// they ever consume the same store.
func mapKey(gx, gy int32) uint64 {
	return uint64(uint32(gx))<<32 | uint64(uint32(gy))
}
