defmodule Boxland.Auth.Players do
  @moduledoc """
  Player registration, authentication, and refresh-token management.
  Players auth via email+password OR OAuth. JWT-style access + refresh
  tokens are minted by `Tokens` and stored hashed here.
  """

  import Ecto.Query
  alias Boxland.Repo
  alias Boxland.Auth.{Player, PlayerOAuthLink, PlayerSession, Password, Tokens}

  # 30 days
  @refresh_ttl_seconds 60 * 60 * 24 * 30
  @refresh_token_bytes 32

  def register_with_password(attrs) do
    pw = attrs[:password] || attrs["password"]

    if is_binary(pw) and byte_size(pw) >= 10 do
      attrs = Map.put(attrs, :password_hash, Password.hash(pw))

      %Player{}
      |> Player.changeset(attrs)
      |> Repo.insert()
    else
      changeset =
        %Player{}
        |> Player.changeset(attrs)
        |> Ecto.Changeset.add_error(:password, "must be at least 10 characters")

      {:error, changeset}
    end
  end

  def register_with_oauth(%{provider: provider, provider_user_id: puid} = attrs) do
    case Repo.one(
           from(l in PlayerOAuthLink,
             where: l.provider == ^provider and l.provider_user_id == ^puid,
             preload: :player
           )
         ) do
      %PlayerOAuthLink{player: player} ->
        {:ok, player}

      nil ->
        Repo.transaction(fn ->
          {:ok, player} =
            %Player{}
            |> Player.changeset(%{email: attrs[:email], display_name: attrs[:display_name]})
            |> Repo.insert()

          {:ok, _link} =
            %PlayerOAuthLink{}
            |> PlayerOAuthLink.changeset(%{
              player_id: player.id,
              provider: provider,
              provider_user_id: puid
            })
            |> Repo.insert()

          player
        end)
    end
  end

  def authenticate_with_password(email, password) when is_binary(email) and is_binary(password) do
    player = Repo.get_by(Player, email: String.downcase(email))

    cond do
      player && Password.verify(player.password_hash, password) ->
        {:ok, player}

      true ->
        # constant-time
        Password.verify(nil, password)
        :error
    end
  end

  def mint_refresh_token(player_id) do
    plain = :crypto.strong_rand_bytes(@refresh_token_bytes) |> Base.url_encode64(padding: false)
    hashed = :crypto.hash(:sha256, plain)

    expires_at =
      DateTime.utc_now() |> DateTime.add(@refresh_ttl_seconds) |> DateTime.truncate(:second)

    case %PlayerSession{}
         |> PlayerSession.changeset(%{
           player_id: player_id,
           refresh_token_hash: hashed,
           expires_at: expires_at
         })
         |> Repo.insert() do
      {:ok, _} -> {:ok, plain}
      {:error, cs} -> {:error, cs}
    end
  end

  @doc """
  Exchange a refresh token for a fresh (access, refresh) pair, rotating
  the refresh token. The old refresh token is deleted atomically.
  Returns {:ok, %{access_token, refresh_token, player}} or :error.
  """
  def refresh(plain_refresh) when is_binary(plain_refresh) do
    token_hash = :crypto.hash(:sha256, plain_refresh)
    now = DateTime.utc_now()

    Repo.transaction(fn ->
      session_q =
        from s in PlayerSession,
          where: s.refresh_token_hash == ^token_hash,
          preload: [:player]

      case Repo.one(session_q) do
        nil ->
          Repo.rollback(:not_found)

        %PlayerSession{player: player, expires_at: e} = session ->
          # DateTime.compare cannot be used in a guard clause; check in body.
          if DateTime.compare(e, now) == :lt do
            Repo.rollback(:expired)
          else
            Repo.delete!(session)
            access = Tokens.mint_player_access(%{player_id: player.id})
            {:ok, new_refresh} = mint_refresh_token(player.id)
            %{access_token: access, refresh_token: new_refresh, player: player}
          end
      end
    end)
    |> case do
      {:ok, payload} -> {:ok, payload}
      {:error, _reason} -> :error
    end
  end
end
