defmodule BoxlandWeb.LevelEditorLiveTest do
  use BoxlandWeb.ConnCase

  import Phoenix.LiveViewTest

  alias Boxland.Auth.Designer
  alias Boxland.{Levels, Maps, Repo}
  alias Boxland.Levels.Level

  setup %{conn: conn} do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{
        email: "editor@example.com",
        password_hash: "x",
        display_name: "Editor"
      })
      |> Repo.insert()

    {:ok, map} =
      Maps.create_map(designer.id, %{
        "slug" => "editor-map",
        "name" => "Editor Map",
        "width" => 8,
        "height" => 8
      })

    {:ok, level} =
      %Level{}
      |> Level.changeset(%{
        owner_id: designer.id,
        slug: "lvl-1",
        name: "Lvl 1",
        map_id: map.id
      })
      |> Repo.insert()

    conn =
      conn
      |> Plug.Test.init_test_session(%{})
      |> BoxlandWeb.DesignerAuth.log_in_designer(designer, "127.0.0.1")

    {:ok, conn: conn, designer: designer, map: map, level: level}
  end

  test "renders the three-pane layout", %{conn: conn, level: level} do
    {:ok, _view, html} = live(conn, ~p"/app/levels/#{level.id}")

    assert html =~ "Level Editor"
    assert html =~ "level-palette"
    assert html =~ "level-canvas"
    assert html =~ "level-inspector"
    assert html =~ "Click an entity on the canvas to inspect."
  end

  test "preset palette places an entity", %{conn: conn, designer: d, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "spawn"})
    render_click(view, "cell", %{"x" => "1", "y" => "2"})

    level_after = Levels.get_level!(d.id, level.id)
    assert length(level_after.entities) == 1
    [e] = level_after.entities
    assert e.pos_x == 32
    assert e.pos_y == 64
    assert e.entity_type.visual_ref == %{"kind" => "preset", "slug" => "spawn"}
  end

  test "invisible palette places a sized invisible box", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "palette_mode", %{"mode" => "invisible"})

    render_change(view, "set_invisible_size", %{"w" => "3", "h" => "2"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities
    assert e.entity_type.visual_ref == %{"kind" => "invisible"}
    assert e.instance_overrides["size"] == %{"w" => 3, "h" => 2}
  end

  test "clicking entity opens inspector and saves tag", %{conn: conn, designer: d, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities

    render_click(view, "select_entity", %{"id" => to_string(e.id)})
    html = render(view)
    assert html =~ "Identity"
    assert html =~ "entity-tag"

    render_change(view, "inspector_save", %{
      "_target" => ["tag"],
      "tag" => "village-sign-1",
      "pos_x" => "0",
      "pos_y" => "0",
      "z_index_override" => ""
    })

    e_after = Levels.get_entity(d.id, level.id, e.id)
    assert e_after.tag == "village-sign-1"
  end

  test "inspector adds and removes a declared property", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})

    render_submit(view, "type_add_property", %{
      "key" => "life",
      "type" => "number",
      "default" => "40"
    })

    e_after = Levels.get_entity(d.id, level.id, e.id)
    assert [%{"key" => "life", "default" => 40}] = e_after.entity_type.properties

    render_click(view, "type_remove_property", %{"key" => "life"})
    e_after2 = Levels.get_entity(d.id, level.id, e.id)
    assert e_after2.entity_type.properties == []
  end

  test "inspector adds an action with default trigger and function", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})

    render_click(view, "type_add_action")

    e_after = Levels.get_entity(d.id, level.id, e.id)
    assert [%{"trigger" => %{"kind" => "spawn"}, "function" => %{"kind" => "despawn_self"}}] =
             e_after.entity_type.actions
  end

  test "inspector updates an action's trigger kind", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})
    render_click(view, "type_add_action")

    [action] = Levels.get_entity(d.id, level.id, e.id).entity_type.actions

    render_change(view, "type_update_action", %{
      "action_id" => action["id"],
      "field" => "trigger.kind",
      "value" => "proximity"
    })

    [updated] = Levels.get_entity(d.id, level.id, e.id).entity_type.actions
    assert updated["trigger"]["kind"] == "proximity"
  end

  test "delete entity removes it and clears selection", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})
    render_click(view, "delete_entity", %{"id" => to_string(e.id)})

    assert Levels.get_level!(d.id, level.id).entities == []
  end

  test "renders the toolbar with select/place/delete buttons", %{conn: conn, level: level} do
    {:ok, _view, html} = live(conn, ~p"/app/levels/#{level.id}")
    assert html =~ ~s(id="level-tool-select")
    assert html =~ ~s(id="level-tool-place")
    assert html =~ ~s(id="level-tool-delete")
  end

  test "select mode is the default and switching tools is observable", %{
    conn: conn,
    level: level
  } do
    {:ok, view, html} = live(conn, ~p"/app/levels/#{level.id}")
    assert html =~ ~r{id="level-tool-select"[^>]*btn-primary}

    html = render_click(view, "tool", %{"tool" => "delete"})
    assert html =~ ~r{id="level-tool-delete"[^>]*btn-primary}
    refute html =~ ~r{id="level-tool-select"[^>]*btn-primary}
  end

  test "select-tool click on a group cell binds-and-selects a group entity", %{
    conn: conn,
    designer: d,
    map: map,
    level: level
  } do
    [layer] = Maps.list_layers(map.id)

    {:ok, _} =
      Maps.update_layer_tiles(layer, %{
        "3,3" => %{
          "asset_id" => 1,
          "tile_index" => 0,
          "rotation" => 0,
          "group_id" => "g-test"
        },
        "4,3" => %{
          "asset_id" => 1,
          "tile_index" => 1,
          "rotation" => 0,
          "group_id" => "g-test"
        }
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    # Default tool is "select". Click a cell that's part of the group.
    render_click(view, "cell", %{"x" => "3", "y" => "3"})

    [e] = Levels.get_level!(d.id, level.id).entities
    assert e.group_id == "g-test"
    # Second click anywhere on the group reuses the same entity (no duplicate).
    render_click(view, "cell", %{"x" => "4", "y" => "3"})
    assert length(Levels.get_level!(d.id, level.id).entities) == 1
  end

  test "select-tool click on a bare tile cell binds-and-selects a tile entity", %{
    conn: conn,
    designer: d,
    map: map,
    level: level
  } do
    [layer] = Maps.list_layers(map.id)

    {:ok, _} =
      Maps.update_layer_tiles(layer, %{
        "5,5" => %{"asset_id" => 7, "tile_index" => 2, "rotation" => 0}
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "cell", %{"x" => "5", "y" => "5"})

    [e] = Levels.get_level!(d.id, level.id).entities
    assert e.entity_type.visual_ref == %{
             "kind" => "tile",
             "asset_id" => 7,
             "tile_index" => 2,
             "rotation" => 0
           }

    assert e.pos_x == 5 * 32
    assert e.pos_y == 5 * 32
  end

  test "delete tool removes the entity at the clicked cell", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "2", "y" => "2"})

    [_e] = Levels.get_level!(d.id, level.id).entities

    render_click(view, "tool", %{"tool" => "delete"})
    render_click(view, "cell", %{"x" => "2", "y" => "2"})

    assert Levels.get_level!(d.id, level.id).entities == []
  end
end
