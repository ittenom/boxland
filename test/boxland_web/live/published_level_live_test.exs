defmodule BoxlandWeb.PublishedLevelLiveTest do
  use BoxlandWeb.ConnCase

  import Phoenix.LiveViewTest

  alias Boxland.Auth.Designer
  alias Boxland.{Levels, Library, Maps, Repo}
  alias Boxland.Levels.Level

  setup do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{
        email: "published@example.com",
        password_hash: "x",
        display_name: "Pub"
      })
      |> Repo.insert()

    {:ok, map} =
      Maps.create_map(designer.id, %{
        "slug" => "pub-map",
        "name" => "Pub Map",
        "width" => 8,
        "height" => 8
      })

    {:ok, level} =
      %Level{}
      |> Level.changeset(%{owner_id: designer.id, slug: "pub-lvl", name: "Pub", map_id: map.id})
      |> Repo.insert()

    {:ok, _spawn} = Levels.create_preset_entity(designer.id, level.id, "spawn", 0, 0)

    {:ok, designer: designer, map: map, level: level}
  end

  defp create_sheet(designer) do
    {:ok, sheet} =
      Library.create_spritesheet(designer.id, %{
        name: "critter",
        sha256: :crypto.strong_rand_bytes(32),
        content_url: "http://example.com/critter.png",
        byte_size: 1024,
        mime_type: "image/png",
        width: 128,
        height: 32
      })

    Library.put_animations(sheet, [
      %{"name" => "walk", "frames" => [0, 1, 2, 3], "fps" => 8, "loop" => true}
    ])
  end

  test "runs the simulation: entities walk their waypoints live", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, sheet} = create_sheet(d)
    {:ok, type} = Levels.ensure_entity_type_for(d.id, {:animated, sheet.id, "walk"})

    {:ok, _walker} =
      Levels.spawn_entity(d.id, level.id, %{
        "entity_type_id" => type.id,
        "pos_x" => 32,
        "pos_y" => 32,
        "waypoints" => [%{"x" => 4, "y" => 1}],
        "movement" => %{"mode" => "loop"}
      })

    {:ok, _version} = Levels.publish_level(d.id, level.id)

    {:ok, view, html} = live(conn, ~p"/play/#{level.id}")

    # Tick 0: walker at its design cell (1,1); tick-synced sprite attrs present.
    assert html =~ "translate(32px, 32px)"
    assert html =~ ~s(data-sprite-sync="tick")
    assert html =~ ~s(data-sprite-frames="0,1,2,3")

    # Advance two ticks: walker steps toward (4,1).
    send(view.pid, :tick)
    send(view.pid, :tick)
    html = render(view)
    assert html =~ "translate(96px, 32px)"
    assert html =~ ~s(data-sprite-tick="2")
  end

  test "ECA transforms run live (mirror on waypoint arrival)", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, sheet} = create_sheet(d)
    {:ok, type} = Levels.ensure_entity_type_for(d.id, {:animated, sheet.id, "walk"})

    {:ok, type} =
      Boxland.Entities.add_action(type, %{
        "name" => "Flip",
        "trigger" => %{"kind" => "waypoint", "index" => "any"},
        "function" => %{"kind" => "transform", "op" => "mirror_x"}
      })

    {:ok, _walker} =
      Levels.spawn_entity(d.id, level.id, %{
        "entity_type_id" => type.id,
        "pos_x" => 0,
        "pos_y" => 32,
        "waypoints" => [%{"x" => 1, "y" => 1}],
        "movement" => %{"mode" => "loop"}
      })

    {:ok, _version} = Levels.publish_level(d.id, level.id)
    {:ok, view, html} = live(conn, ~p"/play/#{level.id}")

    refute html =~ "scale(-1.0, 1.0)"

    # Tick 1: arrival at the waypoint; tick 2: the waypoint trigger fires
    # and the transform mirrors the sprite.
    send(view.pid, :tick)
    send(view.pid, :tick)
    assert render(view) =~ "scale(-1.0, 1.0)"
  end

  test "player movement goes through the sim and respects collision", %{
    conn: conn,
    designer: d,
    level: level
  } do
    {:ok, _block} = Levels.create_preset_entity(d.id, level.id, "collision", 32, 0)
    {:ok, _version} = Levels.publish_level(d.id, level.id)

    {:ok, view, html} = live(conn, ~p"/play/#{level.id}")
    assert html =~ "pub-player"

    # Blocked: (1,0) holds a collision entity. Moves apply on the next tick.
    render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    send(view.pid, :tick)
    assert player_position(render(view)) == "translate(0px, 0px)"

    # Free: down to (0,1).
    render_click(view, "move", %{"dx" => "0", "dy" => "1"})
    send(view.pid, :tick)
    assert player_position(render(view)) == "translate(0px, 32px)"
  end

  test "animated map tiles render with ambient sprite hooks", %{
    conn: conn,
    designer: d,
    map: map,
    level: level
  } do
    {:ok, sheet} = create_sheet(d)
    layer = Maps.primary_layer(map)

    tiles =
      Maps.put_tile(%{}, 2, 2, %{
        asset_id: sheet.id,
        tile_index: 0,
        rotation: 0,
        animation: "walk"
      })

    {:ok, _layer} = Maps.update_layer_tiles(layer, tiles)
    {:ok, _version} = Levels.publish_level(d.id, level.id)

    {:ok, _view, html} = live(conn, ~p"/play/#{level.id}")

    assert html =~ "pub-anim-#{layer.id}-2-2"
    assert html =~ ~s(data-sprite-sync="ambient")
  end

  defp player_position(html) do
    [_, transform] = Regex.run(~r/id="pub-player"[^>]*transform: (translate\([^)]*\))/s, html)
    transform
  end
end
