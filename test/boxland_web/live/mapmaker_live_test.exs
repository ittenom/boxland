defmodule BoxlandWeb.MapmakerLiveTest do
  use BoxlandWeb.ConnCase

  import Phoenix.LiveViewTest

  alias Boxland.Auth.Designer
  alias Boxland.{Maps, Repo}

  setup %{conn: conn} do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{
        email: "mapmaker@example.com",
        password_hash: "x",
        display_name: "Mapper"
      })
      |> Repo.insert()

    {:ok, map} =
      Maps.create_map(designer.id, %{
        "slug" => "test-map",
        "name" => "Test Map",
        "width" => 6,
        "height" => 6
      })

    conn =
      conn
      |> Plug.Test.init_test_session(%{})
      |> BoxlandWeb.DesignerAuth.log_in_designer(designer, "127.0.0.1")

    {:ok, conn: conn, designer: designer, map: map}
  end

  test "renders the layers panel with the primary layer", %{conn: conn, map: map} do
    {:ok, _view, html} = live(conn, ~p"/app/maps/#{map.id}")
    assert html =~ "Layers"
    assert html =~ "ground"
  end

  test "adds a new layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    render_click(view, "add_layer")

    layers = Maps.list_layers(map.id)
    assert length(layers) == 2
    # New layer should be selected and at the top of stack
    [_primary, new] = Enum.sort_by(layers, & &1.z_index)
    assert new.z_index == 1
  end

  test "deletes a layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    render_click(view, "add_layer")

    [_, extra] = Maps.list_layers(map.id)
    render_click(view, "delete_layer", %{"id" => to_string(extra.id)})

    assert length(Maps.list_layers(map.id)) == 1
  end

  test "refuses to delete the last layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)
    html = render_click(view, "delete_layer", %{"id" => to_string(primary.id)})

    assert html =~ "at least one layer"
    assert length(Maps.list_layers(map.id)) == 1
  end

  test "toggles layer visibility", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)
    assert primary.visible

    render_click(view, "toggle_visibility", %{"id" => to_string(primary.id)})
    [primary] = Maps.list_layers(map.id)
    refute primary.visible
  end

  test "toggles layer lock", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)
    refute primary.locked

    render_click(view, "toggle_lock", %{"id" => to_string(primary.id)})
    [primary] = Maps.list_layers(map.id)
    assert primary.locked
  end

  test "renames a layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)

    render_click(view, "rename_layer_start", %{"id" => to_string(primary.id)})
    render_submit(view, "rename_layer", %{"id" => primary.id, "name" => "Floor"})

    [primary] = Maps.list_layers(map.id)
    assert primary.name == "Floor"
  end

  test "moves a layer up reorders z_index", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    render_click(view, "add_layer")

    # display_layers is highest-z first; the new layer is at position 0
    # move it down (lower z) — should drop below primary
    layers = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)
    [primary, top] = layers
    assert top.z_index > primary.z_index

    render_click(view, "move_layer_down", %{"id" => to_string(top.id)})

    layers = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)
    by_id = Elixir.Map.new(layers, &{&1.id, &1})
    assert by_id[top.id].z_index < by_id[primary.id].z_index
  end

  test "select_area_drag sets a normalized rectangle selection", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

    render_hook(view, "select_area_drag", %{"x1" => 3, "y1" => 3, "x2" => 7, "y2" => 5})

    html = render(view)
    assert html =~ ~s|id="map-cell-5-4"|
    # Cell inside the rectangle should have the ring class; outside should not.
    assert html =~ ~r{id="map-cell-5-4"[^>]*ring-primary}
    refute html =~ ~r{id="map-cell-8-6"[^>]*ring-primary}
  end

  test "Escape clears the selection", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 2})
    html = render(view)
    assert html =~ ~r{id="map-cell-1-1"[^>]*ring-primary}

    render_keydown(view, "hotkey", %{"key" => "Escape"})
    html = render(view)
    refute html =~ ~r{id="map-cell-1-1"[^>]*ring-primary}
  end

  describe "selection actions" do
    setup %{designer: designer, map: map} do
      {:ok, tileset} =
        %Boxland.Library.Asset{}
        |> Boxland.Library.Asset.changeset(%{
          owner_id: designer.id,
          kind: "tileset",
          name: "T",
          sha256: String.duplicate("a", 64),
          content_url: "https://example.com/t.png",
          byte_size: 1,
          mime_type: "image/png",
          metadata: %{"columns" => 2, "rows" => 1, "tile_count" => 2}
        })
        |> Boxland.Repo.insert()

      [ground] = Maps.list_layers(map.id)

      tiles =
        %{}
        |> Maps.put_tile(1, 1, %{asset_id: tileset.id, tile_index: 0, rotation: 0})
        |> Maps.put_tile(2, 1, %{asset_id: tileset.id, tile_index: 0, rotation: 0})

      {:ok, ground} = Maps.update_layer_tiles(ground, tiles)

      %{ground: ground, tileset: tileset}
    end

    test "selection_delete clears tiles in the rect", %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_delete")

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      refute Map.has_key?(ground.tiles, "1,1")
      refute Map.has_key?(ground.tiles, "2,1")
    end

    test "selection_rotate on a 1×1 only rotates the tile in place", %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 1, "y2" => 1})
      render_click(view, "selection_rotate")

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      assert ground.tiles["1,1"]["rotation"] == 90

      render_click(view, "selection_rotate")
      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      assert ground.tiles["1,1"]["rotation"] == 180
    end

    test "selection_rotate on a horizontal pair moves tiles into a vertical pair",
         %{conn: conn, map: map, ground: ground, tileset: tileset} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      # Selection covers (1,1)-(2,1) — two horizontal tiles A,B
      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_rotate")

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))

      # After 90° CW around top-left, the two horizontal cells become two vertical
      # cells anchored at the same top-left: (1,1) and (1,2). Tiles A (was at
      # (2,1)) goes to (1,1); B (was at (1,1)) goes to (1,2).
      assert Map.has_key?(ground.tiles, "1,1")
      assert Map.has_key?(ground.tiles, "1,2")
      refute Map.has_key?(ground.tiles, "2,1")

      # Each tile's individual rotation also advances by 90°.
      assert ground.tiles["1,1"]["rotation"] == 90
      assert ground.tiles["1,1"]["asset_id"] == tileset.id
      refute is_nil(ground)
    end

    test "selection_move_begin lifts tiles into move state without touching source yet",
         %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_begin")

      # Source tiles are still in the DB until placement.
      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      assert Map.has_key?(ground.tiles, "1,1")
      assert Map.has_key?(ground.tiles, "2,1")

      # The move-mode banner appears.
      html = render(view)
      assert html =~ "Moving"
      assert html =~ "click to drop"
    end

    test "clicking a cell while in move mode relocates the block",
         %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_begin")

      # Drop at (3, 3) — tiles at (1,1) and (2,1) should land at (3,3) and (4,3).
      render_click(view, "cell", %{"x" => "3", "y" => "3"})

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      refute Map.has_key?(ground.tiles, "1,1")
      refute Map.has_key?(ground.tiles, "2,1")
      assert Map.has_key?(ground.tiles, "3,3")
      assert Map.has_key?(ground.tiles, "4,3")
    end

    test "moving onto a different layer transfers tiles cross-layer",
         %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
      render_click(view, "add_layer")

      [_, upper] = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)
      # ground stays as source (it has the tiles); switch active to upper.
      render_click(view, "select_layer", %{"id" => to_string(ground.id)})
      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_begin")

      # Switch destination layer mid-move and drop.
      render_click(view, "select_layer", %{"id" => to_string(upper.id)})
      render_click(view, "cell", %{"x" => "3", "y" => "3"})

      [from, to] = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)
      assert from.id == ground.id
      refute Map.has_key?(from.tiles, "1,1")
      refute Map.has_key?(from.tiles, "2,1")
      assert Map.has_key?(to.tiles, "3,3")
      assert Map.has_key?(to.tiles, "4,3")
    end

    test "Escape cancels the move without changing tiles",
         %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_begin")
      render_keydown(view, "hotkey", %{"key" => "Escape"})

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      assert Map.has_key?(ground.tiles, "1,1")
      assert Map.has_key?(ground.tiles, "2,1")

      # Banner gone.
      html = render(view)
      refute html =~ "Moving"
    end

    test "selection_copy stores clipboard with the source dimensions and enters clone tool",
         %{conn: conn, map: map} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_copy")

      # Render the canvas while clone tool is active and cursor hovers a cell —
      # the clone-ghost overlay should appear.
      render_hook(view, "cursor_at", %{"x" => 4, "y" => 4})
      html = render(view)

      assert html =~ ~s|id="clone-ghost"|
      # Hover position lands at (4 * 32 = 128, 4 * 32 = 128).
      assert html =~ "top: 128px"
      assert html =~ "left: 128px"
    end

    test "pasting under clone tool drops a copy without removing the source",
         %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_copy")
      render_click(view, "cell", %{"x" => "3", "y" => "3"})

      [ground] = Maps.list_layers(map.id) |> Enum.filter(&(&1.id == ground.id))
      # Source still present.
      assert Map.has_key?(ground.tiles, "1,1")
      assert Map.has_key?(ground.tiles, "2,1")
      # Copy landed.
      assert Map.has_key?(ground.tiles, "3,3")
      assert Map.has_key?(ground.tiles, "4,3")
    end

    test "moving past the map edge is refused",
         %{conn: conn, map: map} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_begin")

      # Map is 6×6; dropping a 2×1 block at (5, 5) would extend to x=6 which is out.
      html = render_click(view, "cell", %{"x" => "5", "y" => "5"})

      assert html =~ "outside the map"
    end

    test "selection_rotate refuses when the rotated rect would leave the map",
         %{conn: conn, map: map} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      # Map is 6×6 (created in outer setup). Select a 6×1 row → rotated would
      # become 1×6, fits. Try a 5×1 starting at row 2 → rotated 1×5 from row 2
      # extends to row 6, off the map.
      render_hook(view, "select_area_drag", %{"x1" => 0, "y1" => 2, "x2" => 4, "y2" => 2})
      html = render_click(view, "selection_rotate")

      assert html =~ "extend past the map edge"
    end

    test "selection_move_up moves tiles to the next layer up", %{conn: conn, map: map, ground: ground} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
      render_click(view, "add_layer")
      # Re-select ground so the move-up source is the layer that has the tiles.
      render_click(view, "select_layer", %{"id" => to_string(ground.id)})

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 2, "y2" => 1})
      render_click(view, "selection_move_up")

      [from, to] = Maps.list_layers(map.id) |> Enum.sort_by(& &1.z_index)

      assert from.id == ground.id
      assert from.tiles == %{}
      assert Map.has_key?(to.tiles, "1,1")
      assert Map.has_key?(to.tiles, "2,1")
    end

    test "selection_move_up with no upper layer flashes an error", %{conn: conn, map: map} do
      {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")

      render_hook(view, "select_area_drag", %{"x1" => 1, "y1" => 1, "x2" => 1, "y2" => 1})
      html = render_click(view, "selection_move_up")

      assert html =~ "No layer above"
    end
  end

  test "set_opacity updates the layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)

    render_change(view, "set_opacity", %{"id" => primary.id, "opacity" => "50"})

    [primary] = Maps.list_layers(map.id)
    assert primary.opacity == 50
  end
end
