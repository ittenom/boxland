defmodule Boxland.LevelsTest do
  use Boxland.DataCase, async: true

  alias Boxland.Worlds.World
  alias Boxland.Levels.{Level, LevelEntity}
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
end
