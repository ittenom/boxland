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

  test "set_opacity updates the layer", %{conn: conn, map: map} do
    {:ok, view, _html} = live(conn, ~p"/app/maps/#{map.id}")
    [primary] = Maps.list_layers(map.id)

    render_change(view, "set_opacity", %{"id" => primary.id, "opacity" => "50"})

    [primary] = Maps.list_layers(map.id)
    assert primary.opacity == 50
  end
end
