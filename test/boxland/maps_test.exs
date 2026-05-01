defmodule Boxland.MapsTest do
  use Boxland.DataCase, async: true

  alias Boxland.Maps.{Map, Layer}

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

  test "valid map produces a valid changeset", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "starter-village",
      name: "Starter Village",
      width: 64,
      height: 64
    }

    changeset = Map.changeset(%Map{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (owner_id, slug) is rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "dup", name: "A", width: 32, height: 32}
    assert {:ok, _} = %Map{} |> Map.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %Map{} |> Map.changeset(attrs) |> Boxland.Repo.insert()
  end

  test "non-positive dimensions rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "bad", name: "Bad", width: 0, height: 64}
    changeset = Map.changeset(%Map{}, attrs)
    refute changeset.valid?
    assert "must be greater than 0" in errors_on(changeset).width
  end

  test "Layer changeset accepts tiles jsonb", %{designer: d} do
    {:ok, map} =
      %Map{}
      |> Map.changeset(%{owner_id: d.id, slug: "m", name: "M", width: 10, height: 10})
      |> Boxland.Repo.insert()

    attrs = %{
      map_id: map.id,
      name: "ground",
      z_index: 0,
      tiles: %{"0,0" => %{"sprite_id" => 1}, "1,0" => %{"sheet_id" => 2, "frame" => 5}}
    }

    changeset = Layer.changeset(%Layer{}, attrs)
    assert changeset.valid?
  end
end
