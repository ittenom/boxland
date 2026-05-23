defmodule Boxland.Entities.EntityTypeTest do
  use Boxland.DataCase, async: true

  alias Boxland.Entities.EntityType

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

  test "minimal valid entity_type", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "goblin", name: "Goblin"}
    changeset = EntityType.changeset(%EntityType{}, attrs)
    assert changeset.valid?
  end

  test "components and scripts default to empty arrays", %{designer: d} do
    {:ok, et} =
      %EntityType{}
      |> EntityType.changeset(%{owner_id: d.id, slug: "barrel", name: "Barrel"})
      |> Boxland.Repo.insert()

    assert et.components == []
    assert et.scripts == []
    assert et.animation_bindings == %{}
  end

  test "accepts components and scripts arrays", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "rich",
      name: "Rich Entity",
      visual_ref: %{"asset_id" => 1},
      animation_bindings: %{"idle" => "default_idle"},
      components: [%{"kind" => "movable", "config" => %{"speed_px_per_sec" => 64}}],
      scripts: [%{"hook" => "on_tick", "source" => %{"type" => "builtin", "action" => "idle"}}]
    }

    changeset = EntityType.changeset(%EntityType{}, attrs)
    assert changeset.valid?
  end

  test "duplicate (owner_id, slug) rejected", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "dup", name: "Dup"}
    assert {:ok, _} = %EntityType{} |> EntityType.changeset(attrs) |> Boxland.Repo.insert()
    assert {:error, _} = %EntityType{} |> EntityType.changeset(attrs) |> Boxland.Repo.insert()
  end

  test "properties default to [] and size to 1x1", %{designer: d} do
    {:ok, et} =
      %EntityType{}
      |> EntityType.changeset(%{owner_id: d.id, slug: "vase", name: "Vase"})
      |> Boxland.Repo.insert()

    assert et.properties == []
    assert et.actions == []
    assert et.size == %{"w" => 1, "h" => 1}
  end

  test "accepts well-formed properties and actions", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "boss",
      name: "Boss",
      size: %{"w" => 2, "h" => 3},
      properties: [
        %{"key" => "life", "type" => "number", "default" => 40},
        %{"key" => "name", "type" => "string", "default" => "Boss"}
      ],
      actions: [
        %{
          "id" => "a1",
          "name" => "On death",
          "enabled" => true,
          "trigger" => %{"kind" => "property", "key" => "life", "op" => "<=", "value" => 0},
          "function" => %{"kind" => "despawn_self"}
        }
      ]
    }

    changeset = EntityType.changeset(%EntityType{}, attrs)
    assert changeset.valid?
  end

  test "rejects malformed property entry", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "bad",
      name: "Bad",
      properties: [%{"key" => "life"}]
    }

    changeset = EntityType.changeset(%EntityType{}, attrs)
    refute changeset.valid?
    assert "each entry needs key, type, default" in errors_on(changeset).properties
  end

  test "rejects malformed action entry", %{designer: d} do
    attrs = %{
      owner_id: d.id,
      slug: "bad2",
      name: "Bad2",
      actions: [%{"id" => "a", "trigger" => %{}, "function" => %{}}]
    }

    changeset = EntityType.changeset(%EntityType{}, attrs)
    refute changeset.valid?
    assert "each action needs id, trigger, function" in errors_on(changeset).actions
  end

  test "rejects non-positive size", %{designer: d} do
    attrs = %{owner_id: d.id, slug: "zero", name: "Zero", size: %{"w" => 0, "h" => 1}}
    changeset = EntityType.changeset(%EntityType{}, attrs)
    refute changeset.valid?
  end
end
