defmodule Boxland.Auth.PlayerTest do
  use Boxland.DataCase, async: true

  alias Boxland.Auth.{Player, PlayerOAuthLink, PlayerSession}

  describe "Player changeset" do
    test "valid attrs with email + password produce a valid changeset" do
      attrs = %{email: "p@example.com", password_hash: "x", display_name: "Player"}
      changeset = Player.changeset(%Player{}, attrs)
      assert changeset.valid?
    end

    test "valid attrs with no email (oauth-only player) produce a valid changeset" do
      changeset = Player.changeset(%Player{}, %{display_name: "OAuthOnly"})
      assert changeset.valid?
    end

    test "missing display_name is invalid" do
      changeset = Player.changeset(%Player{}, %{email: "x@y.com"})
      refute changeset.valid?
      assert "can't be blank" in errors_on(changeset).display_name
    end
  end

  describe "PlayerOAuthLink changeset" do
    setup do
      {:ok, player} =
        %Player{} |> Player.changeset(%{display_name: "P"}) |> Boxland.Repo.insert()

      {:ok, player: player}
    end

    test "valid attrs produce a valid changeset", %{player: p} do
      attrs = %{player_id: p.id, provider: "google", provider_user_id: "google-12345"}
      changeset = PlayerOAuthLink.changeset(%PlayerOAuthLink{}, attrs)
      assert changeset.valid?
    end

    test "duplicate (provider, provider_user_id) is rejected", %{player: p} do
      attrs = %{player_id: p.id, provider: "google", provider_user_id: "shared"}

      assert {:ok, _} =
               %PlayerOAuthLink{} |> PlayerOAuthLink.changeset(attrs) |> Boxland.Repo.insert()

      assert {:error, changeset} =
               %PlayerOAuthLink{} |> PlayerOAuthLink.changeset(attrs) |> Boxland.Repo.insert()

      refute changeset.valid?
    end
  end

  describe "PlayerSession changeset" do
    setup do
      {:ok, player} =
        %Player{} |> Player.changeset(%{display_name: "P"}) |> Boxland.Repo.insert()

      {:ok, player: player}
    end

    test "valid attrs produce a valid changeset", %{player: p} do
      attrs = %{
        player_id: p.id,
        refresh_token_hash: :crypto.hash(:sha256, "rt"),
        expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)
      }

      changeset = PlayerSession.changeset(%PlayerSession{}, attrs)
      assert changeset.valid?
    end
  end
end
