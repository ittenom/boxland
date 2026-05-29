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
    assert html =~ "Click a tile, group, or entity on the canvas to inspect."
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
      "tag" => "village-sign-1"
    })

    e_after = Levels.get_entity(d.id, level.id, e.id)
    assert e_after.tag == "village-sign-1"
  end

  test "inspector position card shows cell units and moves the entity", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "1", "y" => "1"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})

    html = render(view)
    # Inputs are labelled by cell, not px, and reflect cell coords.
    assert html =~ "Position (cell)"
    assert html =~ ~s(id="entity-cell-x")
    assert html =~ ~s(value="1")

    render_change(view, "inspector_position", %{
      "_target" => ["cell_x"],
      "cell_x" => "4",
      "cell_y" => "1",
      "z_index_override" => "25"
    })

    e_after = Levels.get_entity(d.id, level.id, e.id)
    assert e_after.pos_x == 4 * 32
    assert e_after.pos_y == 1 * 32
  end

  test "inspector position card moves a bound group entity's tiles too", %{
    conn: conn,
    designer: d,
    map: map,
    level: level
  } do
    [layer] = Maps.list_layers(map.id)
    gid = "GidXyZ_-1"

    {:ok, _} =
      Maps.update_layer_tiles(layer, %{
        "0,0" => %{"asset_id" => 1, "tile_index" => 0, "rotation" => 0, "group_id" => gid},
        "1,0" => %{"asset_id" => 1, "tile_index" => 1, "rotation" => 0, "group_id" => gid}
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "cell", %{"x" => "0", "y" => "0"})
    render_click(view, "promote_selection")

    [e] = Levels.get_level!(d.id, level.id).entities

    render_change(view, "inspector_position", %{
      "_target" => ["cell_x"],
      "cell_x" => "2",
      "cell_y" => "0",
      "z_index_override" => to_string(e.z_index_override || e.entity_type.default_z_index)
    })

    [layer_after] = Maps.list_layers(map.id)
    # Original positions cleared; tiles relocated by +2 on x.
    assert Boxland.Maps.tile_at(layer_after.tiles, 0, 0) == nil
    assert Boxland.Maps.tile_at(layer_after.tiles, 2, 0)["group_id"] == gid
    assert Boxland.Maps.tile_at(layer_after.tiles, 3, 0)["group_id"] == gid
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

    assert Enum.any?(
             e_after.entity_type.properties,
             &(&1["key"] == "life" and &1["default"] == 40)
           )

    render_click(view, "type_remove_property", %{"key" => "life"})
    e_after2 = Levels.get_entity(d.id, level.id, e.id)
    refute Enum.any?(e_after2.entity_type.properties, &(&1["key"] == "life"))
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

  test "V click on a group cell selects (no entity created) and shows Promote", %{
    conn: conn,
    designer: d,
    map: map,
    level: level
  } do
    [layer] = Maps.list_layers(map.id)
    # Realistic gid format — URL-safe base64 (mixed case + underscore).
    gid = "Ab_-123XyZ"

    {:ok, _} =
      Maps.update_layer_tiles(layer, %{
        "3,3" => %{
          "asset_id" => 1,
          "tile_index" => 0,
          "rotation" => 0,
          "group_id" => gid
        },
        "4,3" => %{
          "asset_id" => 1,
          "tile_index" => 1,
          "rotation" => 0,
          "group_id" => gid
        }
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    html = render_click(view, "cell", %{"x" => "3", "y" => "3"})

    # No entity was created — V is select-only.
    assert Levels.get_level!(d.id, level.id).entities == []
    # The inspector shows the group panel with the Promote button.
    assert html =~ "inspector-group"
    assert html =~ "promote-selection"
    assert html =~ gid

    # Promote creates the entity bound to that group.
    render_click(view, "promote_selection")
    [e] = Levels.get_level!(d.id, level.id).entities
    assert e.group_id == gid
  end

  test "V click on a bare tile cell selects (no entity) and Promote creates one", %{
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
    html = render_click(view, "cell", %{"x" => "5", "y" => "5"})

    assert Levels.get_level!(d.id, level.id).entities == []
    assert html =~ "inspector-tile"
    assert html =~ "promote-selection"

    render_click(view, "promote_selection")
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

  test "V click on an empty cell clears selection", %{conn: conn, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    html = render_click(view, "cell", %{"x" => "0", "y" => "0"})
    assert html =~ "Click a tile, group, or entity on the canvas to inspect."
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

  test "renders the layers panel with the primary layer", %{conn: conn, map: map, level: level} do
    [primary] = Maps.list_layers(map.id)
    {:ok, _view, html} = live(conn, ~p"/app/levels/#{level.id}")

    assert html =~ "Layers"
    assert html =~ ~s(id="layer-row-#{primary.id}")
    assert html =~ primary.name
  end

  test "adding a new layer selects it and updates place_z", %{
    conn: conn,
    map: map,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "add_layer")

    layers = Maps.list_layers(map.id)
    assert length(layers) == 2
    [_primary, new] = Enum.sort_by(layers, & &1.z_index)
    assert new.z_index == 1

    html = render(view)
    assert html =~ ~r{id="layer-row-#{new.id}"[^>]*border-primary}
  end

  test "select_layer marks the row as selected and updates place_z", %{
    conn: conn,
    map: map,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "add_layer")
    [primary, upper] = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)

    render_click(view, "select_layer", %{"id" => to_string(primary.id)})
    html = render(view)
    assert html =~ ~r{id="layer-row-#{primary.id}"[^>]*border-primary}
    refute html =~ ~r{id="layer-row-#{upper.id}"[^>]*border-primary}
  end

  test "toggles layer visibility and lock", %{conn: conn, map: map, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    [primary] = Maps.list_layers(map.id)
    assert primary.visible
    refute primary.locked

    render_click(view, "toggle_visibility", %{"id" => to_string(primary.id)})
    [primary] = Maps.list_layers(map.id)
    refute primary.visible

    render_click(view, "toggle_lock", %{"id" => to_string(primary.id)})
    [primary] = Maps.list_layers(map.id)
    assert primary.locked
  end

  test "refuses to delete the last layer", %{conn: conn, map: map, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    [primary] = Maps.list_layers(map.id)
    html = render_click(view, "delete_layer", %{"id" => to_string(primary.id)})

    assert html =~ "at least one layer"
    assert length(Maps.list_layers(map.id)) == 1
  end

  test "highlights affected layers when a group is selected", %{
    conn: conn,
    map: map,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "add_layer")
    [ground, upper] = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)

    # Put one tile of the group on each layer.
    {:ok, _} =
      Maps.update_layer_tiles(ground, %{
        "1,1" => %{
          "asset_id" => 1,
          "tile_index" => 0,
          "rotation" => 0,
          "group_id" => "gx"
        }
      })

    {:ok, _} =
      Maps.update_layer_tiles(upper, %{
        "2,1" => %{
          "asset_id" => 1,
          "tile_index" => 0,
          "rotation" => 0,
          "group_id" => "gx"
        }
      })

    # Re-mount so the live view picks up the new tiles.
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "cell", %{"x" => "1", "y" => "1"})

    html = render(view)
    assert html =~ ~r{id="layer-row-#{ground.id}"[^>]*ring-accent}
    assert html =~ ~r{id="layer-row-#{upper.id}"[^>]*ring-accent}
  end

  test "Path tool: clicking cells appends waypoints, clicking a waypoint cell removes it", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "1", "y" => "1"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})

    # Switch to the Path tool and lay a 2-waypoint route by clicking cells.
    render_click(view, "tool", %{"tool" => "path"})
    render_click(view, "cell", %{"x" => "5", "y" => "4"})
    render_click(view, "cell", %{"x" => "5", "y" => "6"})

    e2 = Levels.get_entity(d.id, level.id, e.id)
    assert [%{"x" => 5, "y" => 4}, %{"x" => 5, "y" => 6}] = e2.waypoints

    # Movement defaults to loop when waypoints are first added.
    assert e2.movement["mode"] == "loop"

    # Re-clicking an existing waypoint cell removes it.
    render_click(view, "cell", %{"x" => "5", "y" => "4"})
    e3 = Levels.get_entity(d.id, level.id, e.id)
    assert [%{"x" => 5, "y" => 6}] = e3.waypoints

    html = render(view)
    assert html =~ ~s(id="waypoint-marker-#{e.id}-1")
  end

  test "waypoint markers disappear when selection is cleared", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "1", "y" => "1"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})
    render_click(view, "tool", %{"tool" => "path"})
    render_click(view, "cell", %{"x" => "5", "y" => "4"})

    # Switch to select and click empty cell → selection clears, markers gone.
    render_click(view, "tool", %{"tool" => "select"})
    render_click(view, "cell", %{"x" => "7", "y" => "7"})
    html = render(view)
    refute html =~ ~s(id="waypoint-marker-#{e.id}-1")
  end

  test "inspector movement_set changes mode and waypoint_clear_all wipes the route", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "2", "y" => "3"})

    [e] = Levels.get_level!(d.id, level.id).entities
    render_click(view, "select_entity", %{"id" => to_string(e.id)})
    render_click(view, "tool", %{"tool" => "path"})
    render_click(view, "cell", %{"x" => "4", "y" => "3"})

    # Change mode to ping_pong via the form.
    render_change(view, "movement_set", %{
      "mode" => "ping_pong",
      "ticks_per_step" => "2",
      "wait_at_waypoint" => "3"
    })

    updated = Levels.get_entity(d.id, level.id, e.id)
    assert updated.movement["mode"] == "ping_pong"
    assert updated.movement["ticks_per_step"] == 2
    assert updated.movement["wait_at_waypoint"] == 3

    # Clear all waypoints.
    render_click(view, "waypoint_clear_all")
    assert Levels.get_entity(d.id, level.id, e.id).waypoints == []
  end

  test "promoted group entity covers every group cell and pulses when selected", %{
    conn: conn,
    map: map,
    level: level
  } do
    [layer] = Maps.list_layers(map.id)
    gid = "Mixed_Case-1"

    {:ok, _} =
      Maps.update_layer_tiles(layer, %{
        "1,1" => %{"asset_id" => 1, "tile_index" => 0, "rotation" => 0, "group_id" => gid},
        "2,1" => %{"asset_id" => 1, "tile_index" => 0, "rotation" => 0, "group_id" => gid},
        "3,1" => %{"asset_id" => 1, "tile_index" => 0, "rotation" => 0, "group_id" => gid}
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    # Select group, promote, then verify the per-cell coverage.
    render_click(view, "cell", %{"x" => "1", "y" => "1"})
    render_click(view, "promote_selection")

    html = render(view)

    # All three group cells carry an entity overlay tagged with the entity id.
    for x <- 1..3 do
      assert html =~ ~r{level-entity-\d+-cell-#{x}-1}
    end

    # The selection just became {:entity, id} so every cell of that entity
    # gets the pulse class.
    matches = Regex.scan(~r{level-entity-(\d+)-cell-\d+-1[^"]*"[^>]*entity-pulse}, html)
    # Should match all three cells of the same entity id.
    assert length(matches) == 3
    [[_, id1], [_, id2], [_, id3]] = matches
    assert id1 == id2 and id2 == id3
  end

  test "unselected entity cells do not pulse", %{conn: conn, level: level} do
    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "preset", %{"preset" => "sign"})
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    # Click an empty cell to clear selection.
    render_click(view, "tool", %{"tool" => "select"})
    render_click(view, "cell", %{"x" => "5", "y" => "5"})

    html = render(view)
    # The placed entity still exists, but no pulse class on its cell.
    assert html =~ ~r{level-entity-\d+-cell-0-0}
    refute html =~ ~r{level-entity-\d+-cell-0-0[^"]*"[^>]*entity-pulse}
  end

  test "highlights only the affected layer when a bare tile is selected", %{
    conn: conn,
    map: map,
    level: level
  } do
    {:ok, _view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    [ground] = Maps.list_layers(map.id)

    {:ok, _} =
      Maps.update_layer_tiles(ground, %{
        "0,0" => %{"asset_id" => 4, "tile_index" => 1, "rotation" => 0}
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
    render_click(view, "cell", %{"x" => "0", "y" => "0"})

    html = render(view)
    assert html =~ ~r{id="layer-row-#{ground.id}"[^>]*ring-accent}
  end

  describe "rendering chrome" do
    test "gridlines are off by default and toggle on via Show grid", %{conn: conn, level: level} do
      {:ok, view, html} = live(conn, ~p"/app/levels/#{level.id}")

      # No per-cell border by default (production look).
      refute html =~ ~r{id="level-cell-0-0"[^>]*border-base-300}

      html = render_click(view, "toggle_show_grid")
      assert html =~ ~r{id="level-cell-0-0"[^>]*border-base-300}
    end

    test "placed sprite-less entities still render a marker overlay", %{
      conn: conn,
      level: level
    } do
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
      render_click(view, "preset", %{"preset" => "spawn"})
      html = render_click(view, "cell", %{"x" => "1", "y" => "1"})

      assert html =~ ~r{level-entity-\d+-cell-1-1}
    end
  end

  describe "play mode" do
    setup %{designer: d, level: level} do
      {:ok, _spawn} = Levels.create_preset_entity(d.id, level.id, "spawn", 0, 0)
      :ok
    end

    test "Play enters play mode with transport + timeline", %{conn: conn, level: level} do
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
      html = render_click(view, "enter_play")

      assert html =~ ~s(id="play-root")
      assert html =~ ~s(id="play-toggle")
      assert html =~ ~s(id="play-step")
      assert html =~ ~s(id="play-scrubber")
      # Edit-only toolbar is gone in play mode.
      refute html =~ ~s(id="level-toolbar")
    end

    test "step advances the tick and scrub returns to it", %{conn: conn, level: level} do
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
      render_click(view, "enter_play")

      html = render_click(view, "step")
      assert html =~ "1 / 1"

      html = render_click(view, "step")
      assert html =~ "2 / 2"

      # Scrub back to tick 0 (head stays at 2).
      html = render_change(view, "scrub", %{"tick" => "0"})
      assert html =~ "0 / 2"
    end

    test "reset returns to the spawn state", %{conn: conn, level: level} do
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
      render_click(view, "enter_play")
      render_click(view, "step")
      render_click(view, "step")

      html = render_click(view, "play_reset")
      assert html =~ "0 / 0"
    end

    test "a placed sprite entity renders in the keyed sprite layer", %{
      conn: conn,
      designer: d,
      level: level
    } do
      {:ok, _coin} = Levels.create_preset_entity(d.id, level.id, "collectible", 3 * 32, 0)
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")

      [coin] =
        Enum.filter(
          Levels.get_level!(d.id, level.id).entities,
          &(&1.entity_type.slug == "preset-collectible")
        )

      html = render_click(view, "enter_play")
      assert html =~ ~s(id="sim-entity-#{coin.id}")
    end

    test "Edit returns to the editor untouched", %{conn: conn, level: level} do
      {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}")
      render_click(view, "enter_play")
      html = render_click(view, "exit_play")

      assert html =~ ~s(id="level-toolbar")
      refute html =~ ~s(id="play-root")
    end
  end
end
