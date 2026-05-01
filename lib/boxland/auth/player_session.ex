defmodule Boxland.Auth.PlayerSession do
  @moduledoc "A player's refresh-token session. Refresh token hashed."
  use Ecto.Schema
  import Ecto.Changeset

  schema "player_sessions" do
    field :refresh_token_hash, :binary
    field :expires_at, :utc_datetime

    belongs_to :player, Boxland.Auth.Player

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(session, attrs) do
    session
    |> cast(attrs, [:player_id, :refresh_token_hash, :expires_at])
    |> validate_required([:player_id, :refresh_token_hash, :expires_at])
    |> unique_constraint(:refresh_token_hash)
    |> foreign_key_constraint(:player_id)
  end
end
