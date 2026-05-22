defmodule BoxlandWeb.AssetLiveTest do
  use BoxlandWeb.ConnCase

  import Phoenix.LiveViewTest

  alias Boxland.Auth.Designer
  alias Boxland.Library
  alias Boxland.Repo

  setup %{conn: conn} do
    {:ok, designer} =
      %Designer{}
      |> Designer.changeset(%{
        email: "asset-live@example.com",
        password_hash: "x",
        display_name: "Asset Live"
      })
      |> Repo.insert()

    conn =
      conn
      |> Plug.Test.init_test_session(%{})
      |> BoxlandWeb.DesignerAuth.log_in_designer(designer, "127.0.0.1")

    {:ok, conn: conn, designer: designer}
  end

  test "uploads a valid PNG tileset", %{conn: conn, designer: designer} do
    {:ok, view, _html} = live(conn, ~p"/app/assets")

    render_click(view, "open_upload")

    upload =
      file_input(view, "#tileset-upload-form", :tileset, [
        %{
          name: "forest.png",
          content: png_bytes(64, 32),
          type: "image/png"
        }
      ])

    render_upload(upload, "forest.png")

    view
    |> form("#tileset-upload-form", asset: %{name: "Forest"})
    |> render_submit()

    assert [asset] = Library.list_assets(designer.id)
    assert asset.name == "Forest"
    assert asset.metadata["columns"] == 2
    assert asset.metadata["rows"] == 1
  end

  test "renames a tileset", %{conn: conn, designer: designer} do
    {:ok, asset} = create_tileset(designer, "Original")

    {:ok, view, _html} = live(conn, ~p"/app/assets")

    render_click(view, "rename_start", %{"id" => asset.id})
    render_submit(view, "rename_save", %{"id" => asset.id, "name" => "Renamed"})

    assert %{name: "Renamed"} = Library.get_asset!(designer.id, asset.id)
  end

  test "deletes a tileset", %{conn: conn, designer: designer} do
    {:ok, asset} = create_tileset(designer, "Forest")

    {:ok, view, _html} = live(conn, ~p"/app/assets")

    render_click(view, "delete_asset", %{"id" => asset.id})

    assert Library.list_assets(designer.id) == []
  end

  test "paint_pixels event toggles individual mask cells", %{conn: conn, designer: designer} do
    {:ok, asset} = create_tileset(designer, "Forest")

    {:ok, view, _html} = live(conn, ~p"/app/assets")

    render_click(view, "set_mode", %{"mode" => "manual"})
    render_hook(view, "paint_pixels", %{"pixels" => [[0, 0], [1, 1]], "value" => true})

    assert mask = Library.tile_collision(Library.get_asset!(designer.id, asset.id), 0)
    rows = Boxland.Library.CollisionMask.rows(mask)
    assert Enum.at(Enum.at(rows, 0), 0) == true
    assert Enum.at(Enum.at(rows, 1), 1) == true
    assert Enum.at(Enum.at(rows, 2), 2) == false
  end

  defp create_tileset(designer, name) do
    Library.create_tileset(designer.id, %{
      name: name,
      sha256: :crypto.strong_rand_bytes(32),
      content_url: "http://example.com/#{name}.png",
      byte_size: 1024,
      mime_type: "image/png",
      width: 64,
      height: 32,
      tile_indexes: [0, 1]
    })
  end

  defp png_bytes(width, height) do
    <<137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, "IHDR", width::32, height::32, 8, 6, 0, 0, 0,
      0::32>>
  end
end
