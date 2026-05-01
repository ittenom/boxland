defmodule Boxland.Auth.DesignerTest do
  use Boxland.DataCase, async: true

  alias Boxland.Auth.Designer
  alias Boxland.Auth.DesignerSession

  describe "Designer changeset" do
    test "valid attrs produce a valid changeset" do
      attrs = %{
        email: "alice@example.com",
        password_hash: "$argon2id$placeholder",
        display_name: "Alice"
      }
      changeset = Designer.changeset(%Designer{}, attrs)
      assert changeset.valid?
    end

    test "missing email is invalid" do
      changeset = Designer.changeset(%Designer{}, %{password_hash: "x", display_name: "A"})
      refute changeset.valid?
      assert "can't be blank" in errors_on(changeset).email
    end

    test "duplicate email returns a constraint error on insert" do
      attrs = %{email: "dup@example.com", password_hash: "x", display_name: "Dup"}
      assert {:ok, _} = %Designer{} |> Designer.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, changeset} = %Designer{} |> Designer.changeset(attrs) |> Boxland.Repo.insert()
      refute changeset.valid?
      assert "has already been taken" in errors_on(changeset).email
    end
  end

  describe "DesignerSession changeset" do
    setup do
      {:ok, designer} =
        %Designer{}
        |> Designer.changeset(%{email: "ds@example.com", password_hash: "x", display_name: "DS"})
        |> Boxland.Repo.insert()
      {:ok, designer: designer}
    end

    test "valid attrs produce a valid changeset", %{designer: d} do
      attrs = %{
        designer_id: d.id,
        token_hash: :crypto.hash(:sha256, "secret"),
        expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)
      }
      changeset = DesignerSession.changeset(%DesignerSession{}, attrs)
      assert changeset.valid?
    end

    test "duplicate token_hash returns a constraint error on insert", %{designer: d} do
      hash = :crypto.hash(:sha256, "shared-secret")
      attrs = %{designer_id: d.id, token_hash: hash, expires_at: DateTime.utc_now() |> DateTime.add(3600) |> DateTime.truncate(:second)}
      assert {:ok, _} = %DesignerSession{} |> DesignerSession.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, changeset} = %DesignerSession{} |> DesignerSession.changeset(attrs) |> Boxland.Repo.insert()
      refute changeset.valid?
      assert "has already been taken" in errors_on(changeset).token_hash
    end
  end
end
