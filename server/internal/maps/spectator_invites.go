package maps

import (
	"context"
	"errors"
	"fmt"
)

// ErrSpectatorPolicyUnknown is returned when a map row carries an
// unrecognized spectator_policy. Should be impossible thanks to the
// CHECK constraint on the column, but the spectate gate fails closed
// rather than allowing access on garbage data.
var ErrSpectatorPolicyUnknown = errors.New("maps: unknown spectator_policy")

// IsPlayerSpectatorAllowed reports whether the given player may open a
// player-realm spectate connection against the map. Designer-realm
// connections bypass this check and are gated on instance-id namespace
// instead (sandbox: ids reject player realm regardless of policy).
//
// Public maps: any authenticated player.
// Private maps: never via player realm.
// Invite maps: only if (mapID, playerID) exists in map_spectator_invites.
//
// One round-trip in the invite case; zero in the public/private cases.
func (s *Service) IsPlayerSpectatorAllowed(ctx context.Context, mapID, playerID int64, policy string) (bool, error) {
	switch policy {
	case "public":
		return true, nil
	case "private":
		return false, nil
	case "invite":
		var ok bool
		err := s.Pool.QueryRow(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM map_spectator_invites
				WHERE map_id = $1 AND player_id = $2
			)`,
			mapID, playerID,
		).Scan(&ok)
		if err != nil {
			return false, fmt.Errorf("check spectator invite: %w", err)
		}
		return ok, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrSpectatorPolicyUnknown, policy)
	}
}

// GrantSpectatorInvite records that `playerID` may spectate `mapID` when
// the map's spectator_policy is "invite". Idempotent; re-granting the
// same player updates the timestamp + granted-by fields.
func (s *Service) GrantSpectatorInvite(ctx context.Context, mapID, playerID, grantedBy int64) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO map_spectator_invites (map_id, player_id, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (map_id, player_id) DO UPDATE
		SET granted_by = EXCLUDED.granted_by,
		    created_at = now()
	`, mapID, playerID, grantedBy)
	if err != nil {
		return fmt.Errorf("grant spectator invite: %w", err)
	}
	return nil
}

// RevokeSpectatorInvite removes a previously-granted spectator invite.
// No-op if no invite exists for (mapID, playerID).
func (s *Service) RevokeSpectatorInvite(ctx context.Context, mapID, playerID int64) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM map_spectator_invites WHERE map_id = $1 AND player_id = $2`,
		mapID, playerID,
	)
	if err != nil {
		return fmt.Errorf("revoke spectator invite: %w", err)
	}
	return nil
}

// SpectatorInvite is one row from map_spectator_invites. Used by the
// future Designer UI surface that lists outstanding invites.
type SpectatorInvite struct {
	MapID     int64 `json:"map_id"`
	PlayerID  int64 `json:"player_id"`
	GrantedBy int64 `json:"granted_by"`
}

// ListSpectatorInvites returns every invite row for a map, ordered by
// player id. One query; the future invite-management UI will consume it.
func (s *Service) ListSpectatorInvites(ctx context.Context, mapID int64) ([]SpectatorInvite, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT map_id, player_id, granted_by
		   FROM map_spectator_invites
		  WHERE map_id = $1
		  ORDER BY player_id ASC`,
		mapID,
	)
	if err != nil {
		return nil, fmt.Errorf("list spectator invites: %w", err)
	}
	defer rows.Close()

	var out []SpectatorInvite
	for rows.Next() {
		var inv SpectatorInvite
		if err := rows.Scan(&inv.MapID, &inv.PlayerID, &inv.GrantedBy); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}
