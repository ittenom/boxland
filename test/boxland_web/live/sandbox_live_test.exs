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

    {:ok, _spawn} = Levels.create_preset_entity(designer.id, level.id, "spawn", 0, 0)

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

  test "moving the player runs ECA tick and reports despawn", %{
    conn: conn,
    designer: d,
    level: level
  } do
    # Add an entity at (1,0) with a proximity trigger that despawns itself.
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

    # Player at (0,0), trap at (2,0). Out of range. Move +1 to (1,0) — now distance 1, in range.
    html = render_click(view, "move", %{"dx" => "1", "dy" => "0"})
    assert html =~ "despawned"
  end
end
