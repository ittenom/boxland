defmodule BoxlandWeb.SandboxLiveTest do
  use BoxlandWeb.ConnCase

  import Phoenix.LiveViewTest

  alias Boxland.Auth.Designer
  alias Boxland.Entities
  alias Boxland.{Levels, Maps, Repo}
  alias Boxland.Levels.Level

  setup %{conn: conn} do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{
        email: "sb@example.com",
        password_hash: "x",
        display_name: "SB"
      })
      |> Repo.insert()

    {:ok, map} =
      Maps.create_map(designer.id, %{
        "slug" => "sb-map",
        "name" => "SB Map",
        "width" => 8,
        "height" => 8
      })

    {:ok, level} =
      %Level{}
      |> Level.changeset(%{
        owner_id: designer.id,
        slug: "sb-lvl",
        name: "SB",
        map_id: map.id
      })
      |> Repo.insert()

    {:ok, _spawn} =
      Levels.create_preset_entity(designer.id, level.id, "spawn", 0, 0, %{}, z_index_override: 0)

    conn =
      conn
      |> Plug.Test.init_test_session(%{})
      |> BoxlandWeb.DesignerAuth.log_in_designer(designer, "127.0.0.1")

    {:ok, conn: conn, designer: designer, level: level}
  end

  test "renders sandbox with player at spawn cell", %{conn: conn, level: level} do
    {:ok, _view, html} = live(conn, ~p"/app/levels/#{level.id}/sandbox")
    assert html =~ "Sandbox"
  end

  test "stepping after moving the player fires proximity triggers", %{
    conn: conn,
    designer: d,
    level: level
  } do
    # Add an entity at (2,0) with a proximity trigger that despawns itself.
    {:ok, type} = Entities.create_entity_type(d.id, %{"slug" => "trap", "name" => "Trap"})

    {:ok, type} =
      Entities.add_action(type, %{
        "id" => "t",
        "trigger" => %{
          "kind" => "proximity",
          "target" => %{"kind" => "player"},
          "distance" => 1
        },
        "function" => %{"kind" => "despawn_self"}
      })

    {:ok, _e} =
      Levels.spawn_entity(d.id, level.id, %{
        "entity_type_id" => type.id,
        "pos_x" => 64,
        "pos_y" => 0,
        "z_index_override" => 0
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}/sandbox")

    # Player at (0,0), trap at (2,0). Move +1 to (1,0) — distance 1, in range.
    _ = render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    # The sandbox loop now owns time; advance one tick to fire the proximity trigger.
    html = render_click(view, "step")
    assert html =~ "despawned"
  end

  test "proximity triggers do not fire across z-levels", %{
    conn: conn,
    designer: d,
    level: level
  } do
    # Same proximity trap as above, but placed at z=10 while spawn/player are at z=0.
    {:ok, type} =
      Entities.create_entity_type(d.id, %{"slug" => "ztrap", "name" => "ZTrap"})

    {:ok, type} =
      Entities.add_action(type, %{
        "id" => "t",
        "trigger" => %{
          "kind" => "proximity",
          "target" => %{"kind" => "player"},
          "distance" => 1
        },
        "function" => %{"kind" => "despawn_self"}
      })

    {:ok, _e} =
      Levels.spawn_entity(d.id, level.id, %{
        "entity_type_id" => type.id,
        "pos_x" => 64,
        "pos_y" => 0,
        "z_index_override" => 10
      })

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}/sandbox")

    # Player at z=0 walks adjacent to the trap at z=10. Proximity should NOT fire.
    _ = render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    html = render_click(view, "step")
    refute html =~ "despawned"
  end

  test "collision-preset entity at a different z does not block the player", %{
    conn: conn,
    designer: d,
    level: level
  } do
    # Place a collision wall at (1,0) on z=10. Player is at z=0 (from spawn).
    {:ok, _wall} =
      Levels.create_preset_entity(d.id, level.id, "collision", 1 * 32, 0, %{},
        z_index_override: 10
      )

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}/sandbox")

    html = render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    # The wall is on z=10; player is on z=0 — should pass through, not "Blocked".
    refute html =~ "Blocked"
  end

  test "collision-preset entity at the player's z blocks movement", %{
    conn: conn,
    designer: d,
    level: level
  } do
    # Wall at (1,0) on z=0, same as player.
    {:ok, _wall} =
      Levels.create_preset_entity(d.id, level.id, "collision", 1 * 32, 0, %{},
        z_index_override: 0
      )

    {:ok, view, _html} = live(conn, ~p"/app/levels/#{level.id}/sandbox")

    html = render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    assert html =~ "Blocked"
  end
end
