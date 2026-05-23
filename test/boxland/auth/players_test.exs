defmodule Boxland.Auth.PlayersTest do
  use Boxland.DataCase, async: true
  alias Boxland.Auth.Players

  describe "register_with_password/1" do
    test "creates a player with hashed password" do
      assert {:ok, p} =
               Players.register_with_password(%{
                 email: "p@x.com",
                 password: "passwordlong",
                 display_name: "P"
               })

      assert p.id
      assert p.password_hash
    end

    test "rejects short password" do
      assert {:error, _} =
               Players.register_with_password(%{
                 email: "p@x.com",
                 password: "short",
                 display_name: "P"
               })
    end
  end

  describe "register_with_oauth/1" do
    test "creates an oauth-only player and links the identity" do
      assert {:ok, p} =
               Players.register_with_oauth(%{
                 provider: "google",
                 provider_user_id: "g-1",
                 email: "g@x.com",
                 display_name: "Goog"
               })

      assert p.email == "g@x.com"
      assert p.password_hash == nil
    end

    test "second registration with same provider_user_id returns existing player" do
      {:ok, p1} =
        Players.register_with_oauth(%{
          provider: "google",
          provider_user_id: "g-2",
          email: "a@x.com",
          display_name: "X"
        })

      {:ok, p2} =
        Players.register_with_oauth(%{
          provider: "google",
          provider_user_id: "g-2",
          email: "a@x.com",
          display_name: "X"
        })

      assert p1.id == p2.id
    end
  end

  describe "authenticate_with_password/2" do
    setup do
      {:ok, p} =
        Players.register_with_password(%{
          email: "auth@x.com",
          password: "rightpassword",
          display_name: "A"
        })

      {:ok, player: p}
    end

    test "returns {:ok, player} on correct password", %{player: p} do
      assert {:ok, found} = Players.authenticate_with_password("auth@x.com", "rightpassword")
      assert found.id == p.id
    end

    test "returns :error on wrong password" do
      assert :error = Players.authenticate_with_password("auth@x.com", "wrongpassword")
    end

    test "returns :error for an oauth-only account" do
      {:ok, _p} =
        Players.register_with_oauth(%{
          provider: "google",
          provider_user_id: "g-3",
          email: "no-pw@x.com",
          display_name: "X"
        })

      assert :error = Players.authenticate_with_password("no-pw@x.com", "anything")
    end
  end

  describe "mint_refresh_token/1 and refresh/1" do
    setup do
      {:ok, p} =
        Players.register_with_password(%{
          email: "rt@x.com",
          password: "passwordlong",
          display_name: "RT"
        })

      {:ok, player: p}
    end

    test "mint and refresh round-trip", %{player: p} do
      {:ok, plain_refresh} = Players.mint_refresh_token(p.id)
      assert is_binary(plain_refresh)

      assert {:ok, %{access_token: at, refresh_token: new_rt, player: returned}} =
               Players.refresh(plain_refresh)

      assert is_binary(at)
      assert is_binary(new_rt)
      assert returned.id == p.id
      # Old refresh token should be unusable after rotation
      assert :error = Players.refresh(plain_refresh)
    end
  end
end
