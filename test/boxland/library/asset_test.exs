defmodule Boxland.Library.AssetTest do
  use Boxland.DataCase, async: true

  alias Boxland.Library.Asset

  setup do
    {:ok, designer} =
      %Boxland.Auth.Designer{}
      |> Boxland.Auth.Designer.changeset(%{
        email: "d@e.com",
        password_hash: "x",
        display_name: "D"
      })
      |> Boxland.Repo.insert()

    {:ok, designer: designer}
  end

  test "valid sprite asset produces a valid changeset", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "sprite",
      name: "goblin",
      sha256: :crypto.hash(:sha256, "fakebytes"),
      content_url: "https://cdn.example.com/sprites/aa/bb/abcdef.png",
      byte_size: 1234,
      mime_type: "image/png",
      metadata: %{"collision" => %{"kind" => "preset", "value" => "solid"}}
    }

    changeset = Asset.changeset(%Asset{}, attrs)
    assert changeset.valid?
  end

  test "invalid kind is rejected", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "weird",
      name: "x",
      sha256: :crypto.hash(:sha256, "x"),
      content_url: "https://x",
      byte_size: 1,
      mime_type: "image/png",
      metadata: %{}
    }

    changeset = Asset.changeset(%Asset{}, attrs)
    refute changeset.valid?
    assert "is invalid" in errors_on(changeset).kind
  end

  test "duplicate sha256 is rejected", %{designer: d} do
    sha = :crypto.hash(:sha256, "shared")

    attrs = %{
      owner_id: d.id,
      kind: "sprite",
      name: "a",
      sha256: sha,
      content_url: "https://x",
      byte_size: 1,
      mime_type: "image/png",
      metadata: %{}
    }

    assert {:ok, _} = %Asset{} |> Asset.changeset(attrs) |> Boxland.Repo.insert()

    assert {:error, changeset} =
             %Asset{} |> Asset.changeset(%{attrs | name: "b"}) |> Boxland.Repo.insert()

    refute changeset.valid?
  end

  test "spritesheet metadata accepts grid + animations", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "spritesheet",
      name: "hero_walk",
      sha256: :crypto.hash(:sha256, "spritesheet1"),
      content_url: "https://x",
      byte_size: 5000,
      mime_type: "image/png",
      metadata: %{
        "grid_cols" => 4,
        "grid_rows" => 2,
        "animations" => [
          %{"name" => "idle", "frames" => [0, 1, 2, 3], "fps" => 8, "loop" => true}
        ]
      }
    }

    changeset = Asset.changeset(%Asset{}, attrs)
    assert changeset.valid?
  end

  test "tileset asset kind is valid", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      kind: "tileset",
      name: "terrain",
      sha256: :crypto.hash(:sha256, "tileset"),
      content_url: "/uploads/tileset.png",
      byte_size: 1024,
      mime_type: "image/png",
      metadata: %{"tile_size" => 32, "columns" => 2, "rows" => 2, "tile_count" => 4}
    }

    changeset = Asset.changeset(%Asset{}, attrs)
    assert changeset.valid?
  end
end
