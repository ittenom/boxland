defmodule Boxland.Auth.DesignersTest do
  use Boxland.DataCase, async: true
  alias Boxland.Auth.Designers

  describe "register_designer/1" do
    test "creates a designer with a hashed password" do
      assert {:ok, designer} = Designers.register_designer(%{email: "n@example.com", password: "supersecret123", display_name: "New"})
      assert designer.id
      assert designer.password_hash
      assert designer.password_hash != "supersecret123"
    end

    test "rejects short password" do
      assert {:error, changeset} = Designers.register_designer(%{email: "n@x.com", password: "short", display_name: "N"})
      refute changeset.valid?
    end
  end

  describe "authenticate/2" do
    setup do
      {:ok, d} = Designers.register_designer(%{email: "auth@x.com", password: "rightpassword", display_name: "Auth"})
      {:ok, designer: d}
    end

    test "returns {:ok, designer} on correct password", %{designer: d} do
      assert {:ok, returned} = Designers.authenticate("auth@x.com", "rightpassword")
      assert returned.id == d.id
    end

    test "returns :error on wrong password" do
      assert :error = Designers.authenticate("auth@x.com", "wrongpassword")
    end

    test "returns :error on unknown email" do
      assert :error = Designers.authenticate("nobody@x.com", "anything")
    end
  end

  describe "create_session/2 + fetch_session/1" do
    setup do
      {:ok, d} = Designers.register_designer(%{email: "s@x.com", password: "longenough!", display_name: "S"})
      {:ok, designer: d}
    end

    test "create + fetch round-trip stores IP and returns the designer", %{designer: d} do
      {:ok, plain_token} = Designers.create_session(d.id, "127.0.0.1")
      assert is_binary(plain_token)
      assert {:ok, fetched} = Designers.fetch_session(plain_token)
      assert fetched.id == d.id

      # Verify the IP was actually persisted
      token_hash = :crypto.hash(:sha256, plain_token)
      stored = Boxland.Repo.get_by(Boxland.Auth.DesignerSession, token_hash: token_hash)
      assert stored.ip == "127.0.0.1"
    end

    test "fetch_session/1 returns :not_found for unknown token" do
      assert :not_found = Designers.fetch_session("totally-fake-token")
    end
  end
end
