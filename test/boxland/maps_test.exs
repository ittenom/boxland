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

  describe "layer operations" do
    setup %{designer: d} do
      {:ok, map} =
        Boxland.Maps.create_map(d.id, %{
          "slug" => "layered",
          "name" => "Layered",
          "width" => 4,
          "height" => 4
        })

      {:ok, map: map}
    end

    test "create_layer auto-assigns z_index above max and unique name", %{map: map} do
      {:ok, a} = Boxland.Maps.create_layer(map)
      {:ok, b} = Boxland.Maps.create_layer(map)

      assert a.z_index == 1
      assert b.z_index == 2
      assert a.name != b.name
    end

    test "create_layer accepts an explicit name", %{map: map} do
      {:ok, l} = Boxland.Maps.create_layer(map, %{name: "trees"})
      assert l.name == "trees"
    end

    test "delete_layer refuses to remove the last layer", %{map: map} do
      primary = Boxland.Maps.primary_layer(map)
      assert {:error, :last_layer} = Boxland.Maps.delete_layer(primary)
    end

    test "delete_layer works when more than one exists", %{map: map} do
      {:ok, extra} = Boxland.Maps.create_layer(map)
      assert {:ok, _} = Boxland.Maps.delete_layer(extra)
    end

    test "duplicate_layer copies tiles and metadata", %{map: map} do
      primary = Boxland.Maps.primary_layer(map)

      {:ok, primary} =
        Boxland.Maps.update_layer_tiles(primary, %{
          "0,0" => %{"asset_id" => 1, "tile_index" => 0, "rotation" => 0}
        })

      {:ok, dup} = Boxland.Maps.duplicate_layer(primary)

      assert dup.tiles == primary.tiles
      assert dup.id != primary.id
      assert dup.z_index > primary.z_index
    end

    test "toggle_layer_visibility flips visible", %{map: map} do
      l = Boxland.Maps.primary_layer(map)
      assert l.visible
      {:ok, l} = Boxland.Maps.toggle_layer_visibility(l)
      refute l.visible
      {:ok, l} = Boxland.Maps.toggle_layer_visibility(l)
      assert l.visible
    end

    test "toggle_layer_lock flips locked", %{map: map} do
      l = Boxland.Maps.primary_layer(map)
      refute l.locked
      {:ok, l} = Boxland.Maps.toggle_layer_lock(l)
      assert l.locked
    end

    test "set_layer_opacity rejects out-of-range values", %{map: map} do
      l = Boxland.Maps.primary_layer(map)
      assert {:error, _} = Boxland.Maps.set_layer_opacity(l, 150)
      assert {:ok, l} = Boxland.Maps.set_layer_opacity(l, 40)
      assert l.opacity == 40
    end

    test "reorder_layers sets z_index by display order", %{map: map} do
      primary = Boxland.Maps.primary_layer(map)
      {:ok, mid} = Boxland.Maps.create_layer(map, %{name: "mid"})
      {:ok, top} = Boxland.Maps.create_layer(map, %{name: "top"})

      # top-to-bottom display order: [primary, mid, top]
      ids = [primary.id, mid.id, top.id]
      assert {:ok, _} = Boxland.Maps.reorder_layers(map.id, ids)

      layers = Boxland.Maps.list_layers(map.id) |> Enum.sort_by(& &1.id)
      by_id = Elixir.Map.new(layers, &{&1.id, &1})

      assert by_id[primary.id].z_index == 2
      assert by_id[mid.id].z_index == 1
      assert by_id[top.id].z_index == 0
    end
  end
end
