defmodule Boxland.LevelsTest do
  use Boxland.DataCase, async: true

  alias Boxland.Worlds.World
  alias Boxland.Levels.{Level, LevelEntity, PublishedLevelVersion}
  alias Boxland.Maps.Map
  alias Boxland.Entities.EntityType
  alias Boxland.Auth.Designer

  setup do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()

    {:ok, map} =
      %Map{}
      |> Map.changeset(%{owner_id: designer.id, slug: "m", name: "M", width: 32, height: 32})
      |> Boxland.Repo.insert()

    {:ok, et} =
      %EntityType{}
      |> EntityType.changeset(%{owner_id: designer.id, slug: "et", name: "ET"})
      |> Boxland.Repo.insert()

    {:ok, designer: designer, map: map, et: et}
  end

  describe "World" do
    test "minimal valid world", %{designer: d} do
      attrs = %{owner_id: d.id, slug: "starter", name: "Starter"}
      changeset = World.changeset(%World{}, attrs)
      assert changeset.valid?
    end

    test "duplicate (owner_id, slug) rejected", %{designer: d} do
      attrs = %{owner_id: d.id, slug: "w", name: "W"}
      assert {:ok, _} = %World{} |> World.changeset(attrs) |> Boxland.Repo.insert()
      assert {:error, _} = %World{} |> World.changeset(attrs) |> Boxland.Repo.insert()
    end
  end

  describe "Level" do
    test "valid standalone level (no world)", %{designer: d, map: m} do
      attrs = %{owner_id: d.id, slug: "tutorial", name: "Tutorial", map_id: m.id}
      changeset = Level.changeset(%Level{}, attrs)
      assert changeset.valid?
    end

    test "valid level in a world", %{designer: d, map: m} do
      {:ok, w} =
        %World{}
        |> World.changeset(%{owner_id: d.id, slug: "w", name: "W"})
        |> Boxland.Repo.insert()

      attrs = %{owner_id: d.id, slug: "in-world", name: "InW", map_id: m.id, world_id: w.id}
      changeset = Level.changeset(%Level{}, attrs)
      assert changeset.valid?
    end

    test "invalid instancing rejected", %{designer: d, map: m} do
      attrs = %{owner_id: d.id, slug: "x", name: "X", map_id: m.id, instancing: "weird"}
      changeset = Level.changeset(%Level{}, attrs)
      refute changeset.valid?
      assert "is invalid" in errors_on(changeset).instancing
    end
  end

  describe "LevelEntity" do
    setup %{designer: d, map: m} do
      {:ok, level} =
        %Level{}
        |> Level.changeset(%{owner_id: d.id, slug: "lvl", name: "L", map_id: m.id})
        |> Boxland.Repo.insert()

      {:ok, level: level}
    end

    test "valid placement", %{level: lvl, et: et} do
      attrs = %{level_id: lvl.id, entity_type_id: et.id, pos_x: 100, pos_y: 200}
      changeset = LevelEntity.changeset(%LevelEntity{}, attrs)
      assert changeset.valid?
    end

    test "z_index_override is optional", %{level: lvl, et: et} do
      attrs = %{level_id: lvl.id, entity_type_id: et.id, pos_x: 0, pos_y: 0, z_index_override: 50}
      changeset = LevelEntity.changeset(%LevelEntity{}, attrs)
      assert changeset.valid?
    end
  end

  describe "spawn/despawn/move" do
    setup %{designer: d, map: m, et: et} do
      {:ok, level} =
        %Level{}
        |> Level.changeset(%{owner_id: d.id, slug: "spawn-lvl", name: "S", map_id: m.id})
        |> Boxland.Repo.insert()

      {:ok, level: level, et: et}
    end

    test "spawn_entity creates with alive=true", %{designer: d, level: lvl, et: et} do
      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 0,
          "pos_y" => 0
        })

      assert e.script_state["alive"] == true
    end

    test "despawn_entity flips alive=false but keeps row", %{designer: d, level: lvl, et: et} do
      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 0,
          "pos_y" => 0
        })

      assert {:ok, e2} = Boxland.Levels.despawn_entity(d.id, lvl.id, e.id)
      assert e2.script_state["alive"] == false
      assert Boxland.Levels.get_entity(d.id, lvl.id, e.id) != nil
    end

    test "move_entity without group_id just updates pos_x/pos_y/z", %{
      designer: d,
      level: lvl,
      et: et
    } do
      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 0,
          "pos_y" => 0,
          "z_index_override" => 10
        })

      assert {:ok, moved} = Boxland.Levels.move_entity(d.id, lvl.id, e.id, 1, 2, 1)
      assert moved.pos_x == 32
      assert moved.pos_y == 64
      assert moved.z_index_override == 11
    end

    test "move_entity with bound tile group translates tiles across x,y", %{
      designer: d,
      level: lvl,
      map: m,
      et: et
    } do
      {:ok, layer} = Boxland.Maps.create_layer(m, %{name: "ground", z_index: 0})

      {:ok, _} =
        Boxland.Maps.update_layer_tiles(layer, %{
          "5,5" => %{
            "asset_id" => 1,
            "tile_index" => 0,
            "rotation" => 0,
            "group_id" => "g1"
          },
          "6,5" => %{
            "asset_id" => 1,
            "tile_index" => 1,
            "rotation" => 0,
            "group_id" => "g1"
          }
        })

      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 5 * 32,
          "pos_y" => 5 * 32,
          "group_id" => "g1"
        })

      assert {:ok, moved} = Boxland.Levels.move_entity(d.id, lvl.id, e.id, 2, 0, 0)
      assert moved.pos_x == 7 * 32

      [layer_after] = Boxland.Maps.list_layers(m.id)
      assert Boxland.Maps.tile_at(layer_after.tiles, 5, 5) == nil
      assert Boxland.Maps.tile_at(layer_after.tiles, 6, 5) == nil
      assert Boxland.Maps.tile_at(layer_after.tiles, 7, 5)["group_id"] == "g1"
      assert Boxland.Maps.tile_at(layer_after.tiles, 8, 5)["group_id"] == "g1"
    end

    test "move_entity dz relocates tiles to target layer", %{
      designer: d,
      level: lvl,
      map: m,
      et: et
    } do
      {:ok, layer_a} = Boxland.Maps.create_layer(m, %{name: "ground", z_index: 0})
      {:ok, layer_b} = Boxland.Maps.create_layer(m, %{name: "upper", z_index: 1})

      {:ok, _} =
        Boxland.Maps.update_layer_tiles(layer_a, %{
          "3,3" => %{
            "asset_id" => 1,
            "tile_index" => 0,
            "rotation" => 0,
            "group_id" => "g2"
          }
        })

      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 3 * 32,
          "pos_y" => 3 * 32,
          "z_index_override" => 0,
          "group_id" => "g2"
        })

      assert {:ok, moved} = Boxland.Levels.move_entity(d.id, lvl.id, e.id, 0, 0, 1)
      assert moved.z_index_override == 1

      layer_a_after = Boxland.Maps.get_layer!(layer_a.id)
      layer_b_after = Boxland.Maps.get_layer!(layer_b.id)
      assert Boxland.Maps.tile_at(layer_a_after.tiles, 3, 3) == nil
      assert Boxland.Maps.tile_at(layer_b_after.tiles, 3, 3)["group_id"] == "g2"
    end

    test "move_entity fails with :no_target_layer when target z has no layer", %{
      designer: d,
      level: lvl,
      map: m,
      et: et
    } do
      {:ok, layer_a} = Boxland.Maps.create_layer(m, %{name: "ground", z_index: 0})

      {:ok, _} =
        Boxland.Maps.update_layer_tiles(layer_a, %{
          "0,0" => %{
            "asset_id" => 1,
            "tile_index" => 0,
            "rotation" => 0,
            "group_id" => "g3"
          }
        })

      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 0,
          "pos_y" => 0,
          "z_index_override" => 0,
          "group_id" => "g3"
        })

      assert {:error, :no_target_layer} =
               Boxland.Levels.move_entity(d.id, lvl.id, e.id, 0, 0, 5)

      # No partial writes
      [layer_after] = Boxland.Maps.list_layers(m.id)
      assert Boxland.Maps.tile_at(layer_after.tiles, 0, 0)["group_id"] == "g3"
    end

    test "bind_group snaps pos to bbox top-left", %{
      designer: d,
      level: lvl,
      map: m,
      et: et
    } do
      {:ok, layer} = Boxland.Maps.create_layer(m, %{name: "ground", z_index: 0})

      {:ok, _} =
        Boxland.Maps.update_layer_tiles(layer, %{
          "10,12" => %{
            "asset_id" => 1,
            "tile_index" => 0,
            "rotation" => 0,
            "group_id" => "gbox"
          },
          "11,13" => %{
            "asset_id" => 1,
            "tile_index" => 1,
            "rotation" => 0,
            "group_id" => "gbox"
          }
        })

      {:ok, e} =
        Boxland.Levels.spawn_entity(d.id, lvl.id, %{
          "entity_type_id" => et.id,
          "pos_x" => 0,
          "pos_y" => 0
        })

      assert {:ok, bound} = Boxland.Levels.bind_group(d.id, lvl.id, e.id, "gbox")
      assert bound.group_id == "gbox"
      assert bound.pos_x == 10 * 32
      assert bound.pos_y == 12 * 32
    end
  end

  describe "publishing" do
    setup %{designer: d, map: m} do
      {:ok, level} =
        %Level{}
        |> Level.changeset(%{
          owner_id: d.id,
          slug: "publishable",
          name: "Publishable",
          map_id: m.id
        })
        |> Boxland.Repo.insert()

      {:ok, level: level}
    end

    test "requires a spawn point", %{designer: d, level: level} do
      assert {:error, "Add a spawn point before publishing."} =
               Boxland.Levels.publish_level(d.id, level.id)
    end

    test "creates immutable version snapshots", %{designer: d, level: level} do
      assert {:ok, _spawn} = Boxland.Levels.create_preset_entity(d.id, level.id, "spawn", 0, 0)

      assert {:ok, version} = Boxland.Levels.publish_level(d.id, level.id)
      assert version.version == 1
      assert version.snapshot["level"]["slug"] == "publishable"
      assert [%{"preset" => "spawn"}] = version.snapshot["entities"]

      assert %PublishedLevelVersion{} = Boxland.Levels.latest_published_version(level.id)
    end
  end
end
