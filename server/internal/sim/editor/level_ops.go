package editor

import (
	"context"
	"encoding/json"
	"fmt"

	"boxland/server/internal/levels"
)

// level_ops.go — concrete `Op` implementations for the level
// editor's placement CRUD. Each op persists through
// `levels.Service` and computes the inverse needed for undo.

// ---- PlaceLevelEntityOp --------------------------------------------

// PlaceLevelEntityOp adds one placement to a level. Apply persists
// via levels.PlaceEntity; Inverse is a RemoveLevelEntityOp keyed
// on the freshly-assigned id (which we capture from Apply's return
// value into the Op's `placedID` field).
type PlaceLevelEntityOp struct {
	LevelID         int64
	EntityTypeID    int64
	X, Y            int32
	RotationDegrees int16
	Overrides       json.RawMessage
	Tags            []string

	placedID int64 // populated by Apply; read by Inverse
}

func (o *PlaceLevelEntityOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Levels == nil {
		return Diff{}, fmt.Errorf("PlaceLevelEntityOp: Levels service required")
	}
	in := levels.PlaceEntityInput{
		LevelID: o.LevelID, EntityTypeID: o.EntityTypeID,
		X: o.X, Y: o.Y,
		RotationDegrees: o.RotationDegrees,
		InstanceOverridesJSON: o.Overrides,
		Tags: o.Tags,
	}
	le, err := deps.Levels.PlaceEntity(ctx, in)
	if err != nil {
		return Diff{}, fmt.Errorf("place entity: %w", err)
	}
	o.placedID = le.ID
	return Diff{
		Kind: DiffPlacementAdded,
		Body: le,
	}, nil
}

func (o *PlaceLevelEntityOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if o.placedID == 0 {
		return nil, fmt.Errorf("PlaceLevelEntityOp.Inverse: Apply not run")
	}
	return &RemoveLevelEntityOp{LevelID: o.LevelID, PlacementID: o.placedID}, nil
}

func (o *PlaceLevelEntityOp) Describe() string {
	return fmt.Sprintf("place entity_type=%d at (%d,%d)", o.EntityTypeID, o.X, o.Y)
}

// ---- MoveLevelEntityOp ---------------------------------------------

// MoveLevelEntityOp updates a placement's x/y/rotation. Inverse
// captures the previous position read from the DB at Apply time.
type MoveLevelEntityOp struct {
	LevelID         int64
	PlacementID     int64
	X, Y            int32
	RotationDegrees int16

	prevX, prevY    int32
	prevRotation    int16
}

func (o *MoveLevelEntityOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Levels == nil {
		return Diff{}, fmt.Errorf("MoveLevelEntityOp: Levels service required")
	}
	current, err := findLevelEntity(ctx, deps, o.LevelID, o.PlacementID)
	if err != nil {
		return Diff{}, err
	}
	o.prevX = current.X
	o.prevY = current.Y
	o.prevRotation = current.RotationDegrees
	if err := deps.Levels.MoveEntity(ctx, o.PlacementID, o.X, o.Y, o.RotationDegrees); err != nil {
		return Diff{}, fmt.Errorf("move entity: %w", err)
	}
	updated := *current
	updated.X = o.X
	updated.Y = o.Y
	updated.RotationDegrees = o.RotationDegrees
	return Diff{
		Kind: DiffPlacementMoved,
		Body: &updated,
	}, nil
}

func (o *MoveLevelEntityOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	return &MoveLevelEntityOp{
		LevelID: o.LevelID, PlacementID: o.PlacementID,
		X: o.prevX, Y: o.prevY, RotationDegrees: o.prevRotation,
	}, nil
}

func (o *MoveLevelEntityOp) Describe() string {
	return fmt.Sprintf("move placement=%d to (%d,%d) rot=%d",
		o.PlacementID, o.X, o.Y, o.RotationDegrees)
}

// ---- RemoveLevelEntityOp -------------------------------------------

// RemoveLevelEntityOp deletes a placement. Inverse is a
// PlaceLevelEntityOp populated from the placement's full pre-image.
type RemoveLevelEntityOp struct {
	LevelID     int64
	PlacementID int64

	prevSnapshot *levels.LevelEntity
}

func (o *RemoveLevelEntityOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Levels == nil {
		return Diff{}, fmt.Errorf("RemoveLevelEntityOp: Levels service required")
	}
	current, err := findLevelEntity(ctx, deps, o.LevelID, o.PlacementID)
	if err != nil {
		return Diff{}, err
	}
	snap := *current
	o.prevSnapshot = &snap
	if err := deps.Levels.RemoveEntity(ctx, o.PlacementID); err != nil {
		return Diff{}, fmt.Errorf("remove entity: %w", err)
	}
	return Diff{Kind: DiffPlacementRemoved, Body: o.PlacementID}, nil
}

func (o *RemoveLevelEntityOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	if o.prevSnapshot == nil {
		return nil, fmt.Errorf("RemoveLevelEntityOp.Inverse: Apply not run")
	}
	prev := o.prevSnapshot
	return &PlaceLevelEntityOp{
		LevelID:         o.LevelID,
		EntityTypeID:    prev.EntityTypeID,
		X:               prev.X,
		Y:               prev.Y,
		RotationDegrees: prev.RotationDegrees,
		Overrides:       prev.InstanceOverridesJSON,
		Tags:            prev.Tags,
	}, nil
}

func (o *RemoveLevelEntityOp) Describe() string {
	return fmt.Sprintf("remove placement=%d", o.PlacementID)
}

// ---- SetLevelEntityOverridesOp -------------------------------------

type SetLevelEntityOverridesOp struct {
	LevelID     int64
	PlacementID int64
	Overrides   json.RawMessage

	prevOverrides json.RawMessage
}

func (o *SetLevelEntityOverridesOp) Apply(ctx context.Context, deps Deps) (Diff, error) {
	if deps.Levels == nil {
		return Diff{}, fmt.Errorf("SetLevelEntityOverridesOp: Levels service required")
	}
	current, err := findLevelEntity(ctx, deps, o.LevelID, o.PlacementID)
	if err != nil {
		return Diff{}, err
	}
	o.prevOverrides = append(json.RawMessage(nil), current.InstanceOverridesJSON...)
	if err := deps.Levels.SetEntityOverrides(ctx, o.PlacementID, o.Overrides); err != nil {
		return Diff{}, fmt.Errorf("set overrides: %w", err)
	}
	return Diff{
		Kind: DiffOverridesChanged,
		Body: struct {
			PlacementID int64
			Overrides   json.RawMessage
		}{o.PlacementID, o.Overrides},
	}, nil
}

func (o *SetLevelEntityOverridesOp) Inverse(_ context.Context, _ Deps) (Op, error) {
	return &SetLevelEntityOverridesOp{
		LevelID: o.LevelID, PlacementID: o.PlacementID,
		Overrides: o.prevOverrides,
	}, nil
}

func (o *SetLevelEntityOverridesOp) Describe() string {
	return fmt.Sprintf("set overrides on placement=%d", o.PlacementID)
}

// ---- helpers --------------------------------------------------------

// findLevelEntity scopes a placement lookup by level. Mirrors the
// HTTP handler's tenant-isolation pattern: returns ErrLevelEntityNotFound
// when the placement exists but belongs to a different level.
func findLevelEntity(ctx context.Context, deps Deps, levelID, placementID int64) (*levels.LevelEntity, error) {
	if deps.Levels == nil {
		return nil, fmt.Errorf("Levels service required")
	}
	all, err := deps.Levels.ListEntities(ctx, levelID)
	if err != nil {
		return nil, fmt.Errorf("list entities: %w", err)
	}
	for i := range all {
		if all[i].ID == placementID {
			return &all[i], nil
		}
	}
	return nil, levels.ErrLevelEntityNotFound
}
