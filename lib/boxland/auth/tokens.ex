defmodule Boxland.Auth.Tokens do
  @moduledoc """
  Phoenix.Token wrappers for game socket auth. Two mintable token kinds:

    * Player access — short-lived (15 min), realm: :player
    * Designer-as-sandbox — short-lived (30 min), realm: :designer_sandbox,
      scoped to a single level_id

  Both verify through `verify_game_token/1`, which the GameSocket uses
  to accept either kind. Realm distinguishes downstream authorization.
  """

  @endpoint BoxlandWeb.Endpoint
  @namespace "game_token"
  @player_max_age 900           # 15 minutes
  @sandbox_max_age 1800         # 30 minutes

  @doc "Mint a player access token. `claims` must include `:player_id`."
  def mint_player_access(%{player_id: pid}) do
    Phoenix.Token.sign(@endpoint, @namespace, %{
      player_id: pid,
      realm: :player,
      iat: System.system_time(:second)
    })
  end

  @doc "Mint a designer-as-sandbox token, scoped to one level."
  def mint_sandbox(%{designer_id: did, level_id: lid}) do
    Phoenix.Token.sign(@endpoint, @namespace, %{
      player_id: did,
      realm: :designer_sandbox,
      level_id: lid,
      iat: System.system_time(:second)
    })
  end

  @doc """
  Verify any kind of game token. Returns the decoded claims or
  `{:error, reason}`. Enforces max_age based on the realm field.
  """
  def verify_game_token(token) do
    with {:ok, claims} <- Phoenix.Token.verify(@endpoint, @namespace, token, max_age: @sandbox_max_age),
         :ok <- enforce_age_for_realm(claims) do
      {:ok, claims}
    end
  end

  defp enforce_age_for_realm(%{realm: :player, iat: iat}) do
    if System.system_time(:second) - iat <= @player_max_age, do: :ok, else: {:error, :expired}
  end
  defp enforce_age_for_realm(%{realm: :designer_sandbox}), do: :ok
  defp enforce_age_for_realm(_), do: {:error, :invalid_claims}
end
