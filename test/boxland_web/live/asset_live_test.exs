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

  defp png_bytes(width, height) do
    <<137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, "IHDR", width::32, height::32, 8, 6, 0, 0, 0,
      0::32>>
  end
end
