package ws

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"boxland/server/internal/proto"
	"boxland/server/internal/sim/aoi"
	"boxland/server/internal/sim/spatial"
)

// Spectate authorization errors. Stable identity so dispatcher logs +
// future Diff "rejection" frames can key off them.
var (
	ErrSpectateLevelNotFound = errors.New("spectate: level not found")
	ErrSpectatePrivate       = errors.New("spectate: level is private; designer realm required")
	ErrSpectateNotInvited    = errors.New("spectate: invite required for this level")
	ErrSpectateSandboxRealm  = errors.New("spectate: sandbox instance requires designer realm")
)

// handleSpectateReal replaces the RegisterDefaultVerbs stub. It opens
// an entity-less AOI subscription on the named level under the Spectator
// broadcaster policy. The connection's realm must already be one of
// {player, designer} from the Auth handshake; this handler enforces the
// per-level spectator_policy on top.
//
// Authorization rules (PLAN.md §4m):
//
//   * sandbox:* instance ids: designer realm only, regardless of level
//     spectator_policy. The sandbox id namespace is designer-private by
//     definition (PLAN.md §1 sandbox-vs-live).
//   * spectator_policy="public":  any authenticated realm.
//   * spectator_policy="private": designer realm only.
//   * spectator_policy="invite":  designer realm OR an explicit invite
//     row for (levelID, playerID) in level_spectator_invites.
//
// On success the connection's Subscription is replaced with one carrying
// PolicySpectator, which the broadcaster picks up automatically (see
// Broadcaster.PolicyFor). FollowPlayer-mode targets are recorded on the
// subscription's FollowTarget for the runtime to re-centre per tick;
// FreeCam mode leaves it zero so the camera stays where the client
// places it.
func handleSpectateReal(deps AuthoringDeps) VerbHandler {
	return func(ctx context.Context, conn *Connection, payload []byte) error {
		if conn.Realm() != RealmPlayer && conn.Realm() != RealmDesigner {
			return errors.New("spectate: requires authenticated realm")
		}
		if len(payload) < 8 {
			return errors.New("spectate: short payload")
		}
		sp := proto.GetRootAsSpectatePayload(payload, 0)

		// SpectatePayload's MapId() field now carries a LEVEL id (the
		// FlatBuffers field name is preserved for wire-protocol
		// compatibility; semantics moved to levels in the redesign).
		levelID := sp.MapId()
		if levelID == 0 {
			return errors.New("spectate: level_id is required")
		}

		lv, err := deps.LevelsService.FindByID(ctx, int64(levelID))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSpectateLevelNotFound, err)
		}

		// We still need the underlying map for tile geometry (width /
		// height) so we can centre the camera. Levels reference exactly
		// one map.
		m, err := deps.MapsService.FindByID(ctx, lv.MapID)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSpectateLevelNotFound, err)
		}

		instanceID := canonicalInstanceID(levelID)
		if hint := strings.TrimSpace(string(sp.InstanceHint())); hint != "" {
			instanceID = hint
		}

		// Sandbox instances are designer-only regardless of level policy.
		// This is the same realm-isolation invariant PLAN.md §4j calls
		// out for the AOI subscription manager.
		if strings.HasPrefix(instanceID, "sandbox:") && conn.Realm() != RealmDesigner {
			return ErrSpectateSandboxRealm
		}

		// Designer realm bypasses spectator_policy entirely (designers
		// can always observe their own levels); player realm must satisfy
		// the policy.
		if conn.Realm() == RealmPlayer {
			ok, err := deps.LevelsService.IsPlayerSpectatorAllowed(
				ctx, int64(levelID), int64(conn.Subject()), lv.SpectatorPolicy,
			)
			if err != nil {
				return fmt.Errorf("spectate: authorize: %w", err)
			}
			if !ok {
				switch lv.SpectatorPolicy {
				case "private":
					return ErrSpectatePrivate
				case "invite":
					return ErrSpectateNotInvited
				default:
					return fmt.Errorf("spectate: not authorized for policy %q", lv.SpectatorPolicy)
				}
			}
		}

		// Bring the live instance up if nobody's joined yet. Spectators
		// pay the recovery cost the same as the first player would; the
		// per-key build barrier in InstanceManager makes this cheap on
		// concurrent spectate joins for the same instance.
		mi, err := deps.Instances.GetOrCreate(ctx, levelID, instanceID)
		if err != nil {
			return fmt.Errorf("spectate: get-or-create instance: %w", err)
		}
		_ = mi

		// Centre the camera. FollowPlayer points at the target's last
		// known chunk if we have one; FreeCam (and FollowPlayer with
		// no live target) drops onto the map's middle.
		centre := spatial.MakeChunkID(
			(m.Width/spatial.ChunkTiles)/2,
			(m.Height/spatial.ChunkTiles)/2,
		)
		// Future refinement: look up the followed player's entity and
		// snap to its chunk; for v1 the client re-centres via SetFocus
		// after the first Snapshot.

		conn.Subscription = newSubscriptionForConn(conn, aoi.PolicySpectator, centre)
		conn.Subscription.FollowTarget = sp.TargetPlayerId()
		conn.Subscription.FreeCam = sp.Mode() == proto.SpectateModeFreeCam

		slog.Info("spectate subscribed",
			"conn", conn.ID(),
			"realm", conn.Realm(),
			"level_id", levelID,
			"instance_id", instanceID,
			"mode", sp.Mode(),
			"target_player_id", sp.TargetPlayerId(),
			"policy", lv.SpectatorPolicy,
		)
		return nil
	}
}

// RegisterSpectatorVerb replaces the stub Spectate handler from
// RegisterDefaultVerbs with the real authoring-aware handler. Call AFTER
// RegisterDefaultVerbs so the override sticks.
//
// Kept as its own registration so test setups that don't need spectate
// (most of them) can opt out.
func RegisterSpectatorVerb(d *Dispatcher, deps AuthoringDeps) {
	d.playerHandlers[proto.VerbSpectate] = handleSpectateReal(deps)
}
