defmodule Boxland.Game.LevelStateTest do
  use Boxland.DataCase, async: true

  alias Boxland.Game.LevelState
  alias Boxland.Auth.Designer
  alias Boxland.Maps.Map
  alias Boxland.Levels.Level

  setup do
    {:ok, d} =
      %Designer{}
      |> Designer.changeset(%{email: "d@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()

    {:ok, m} =
      %Map{}
      |> Map.changeset(%{owner_id: d.id, slug: "m", name: "M", width: 10, height: 10})
      |> Boxland.Repo.insert()

    {:ok, lvl} =
      %Level{}
      |> Level.changeset(%{owner_id: d.id, slug: "l", name: "L", map_id: m.id})
      |> Boxland.Repo.insert()

    {:ok, level: lvl}
  end

  test "valid state row", %{level: lvl} do
    attrs = %{
      level_id: lvl.id,
      instance_key: "shared",
      state: <<0, 1, 2>>,
      flushed_at: DateTime.utc_now() |> DateTime.truncate(:second)
    }

    changeset = LevelState.changeset(%LevelState{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (level_id, instance_key) rejected", %{level: lvl} do
    attrs = %{
      level_id: lvl.id,
      instance_key: "shared",
      state: <<0>>,
      flushed_at: DateTime.utc_now() |> DateTime.truncate(:second)
    }

    assert {:ok, _} = %LevelState{} |> LevelState.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %LevelState{} |> LevelState.changeset(attrs) |> Boxland.Repo.insert()
  end
end
