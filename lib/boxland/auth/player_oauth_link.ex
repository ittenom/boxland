defmodule Boxland.Auth.PlayerOAuthLink do
  @moduledoc "Links a Player to an external OAuth identity (provider + provider_user_id)."
  use Ecto.Schema
  import Ecto.Changeset

  @valid_providers ~w(google apple discord)

  schema "player_oauth_links" do
    field :provider, :string
    field :provider_user_id, :string

    belongs_to :player, Boxland.Auth.Player

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(link, attrs) do
    link
    |> cast(attrs, [:player_id, :provider, :provider_user_id])
    |> validate_required([:player_id, :provider, :provider_user_id])
    |> validate_inclusion(:provider, @valid_providers)
    |> unique_constraint([:provider, :provider_user_id], name: :player_oauth_links_provider_provider_user_id_index)
    |> foreign_key_constraint(:player_id)
  end
end
