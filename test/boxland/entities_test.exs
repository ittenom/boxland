defmodule Boxland.EntitiesTest do
  use Boxland.DataCase, async: true

  alias Boxland.Entities
  alias Boxland.Entities.EntityType
  alias Boxland.Auth.Designer

  setup do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{email: "ent@e.com", password_hash: "x", display_name: "D"})
      |> Boxland.Repo.insert()

    {:ok, designer: designer}
  end

  describe "create_entity_type/2" do
    test "inserts with owner_id", %{designer: d} do
      assert {:ok, %EntityType{slug: "minion"}} =
               Entities.create_entity_type(d.id, %{"slug" => "minion", "name" => "Minion"})
    end
  end

  describe "merge_properties/2" do
    test "instance overrides win, declared defaults fill gaps", %{designer: d} do
      {:ok, type} =
        Entities.create_entity_type(d.id, %{
          "slug" => "merge",
          "name" => "Merge",
          "properties" => [
            %{"key" => "life", "type" => "number", "default" => 40},
            %{"key" => "money", "type" => "number", "default" => 0}
          ]
        })

      merged = Entities.merge_properties(type, %{"life" => 12, "extra" => "bonus"})
      assert merged == %{"life" => 12, "money" => 0, "extra" => "bonus"}
    end

    test "empty overrides returns declared defaults", %{designer: d} do
      {:ok, type} =
        Entities.create_entity_type(d.id, %{
          "slug" => "defaults",
          "name" => "D",
          "properties" => [%{"key" => "hp", "type" => "number", "default" => 5}]
        })

      assert Entities.merge_properties(type, %{}) == %{"hp" => 5}
    end
  end

  describe "actions" do
    setup %{designer: d} do
      {:ok, type} = Entities.create_entity_type(d.id, %{"slug" => "a", "name" => "A"})
      {:ok, type: type}
    end

    test "add_action assigns id, enabled, name defaults", %{type: type} do
      {:ok, type} =
        Entities.add_action(type, %{
          "trigger" => %{"kind" => "spawn"},
          "function" => %{"kind" => "despawn_self"}
        })

      [a] = type.actions
      assert is_binary(a["id"])
      assert a["enabled"] == true
      assert a["name"] == "Action"
    end

    test "update_action merges attrs", %{type: type} do
      {:ok, type} =
        Entities.add_action(type, %{
          "id" => "fixed",
          "trigger" => %{"kind" => "spawn"},
          "function" => %{"kind" => "despawn_self"}
        })

      {:ok, type} = Entities.update_action(type, "fixed", %{"enabled" => false})
      assert [%{"enabled" => false, "id" => "fixed"}] = type.actions
    end

    test "remove_action drops by id", %{type: type} do
      {:ok, type} =
        Entities.add_action(type, %{
          "id" => "x",
          "trigger" => %{"kind" => "spawn"},
          "function" => %{"kind" => "despawn_self"}
        })

      {:ok, type} = Entities.remove_action(type, "x")
      assert type.actions == []
    end
  end

  describe "properties (schema)" do
    setup %{designer: d} do
      {:ok, type} = Entities.create_entity_type(d.id, %{"slug" => "p", "name" => "P"})
      {:ok, type: type}
    end

    test "add_property rejects duplicates", %{type: type} do
      {:ok, type} =
        Entities.add_property(type, %{"key" => "hp", "type" => "number", "default" => 1})

      assert {:error, :duplicate_key} =
               Entities.add_property(type, %{
                 "key" => "hp",
                 "type" => "number",
                 "default" => 2
               })
    end

    test "remove_property drops by key", %{type: type} do
      {:ok, type} =
        Entities.add_property(type, %{"key" => "hp", "type" => "number", "default" => 1})

      {:ok, type} = Entities.remove_property(type, "hp")
      assert type.properties == []
    end
  end
end
