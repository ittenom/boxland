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
end
