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

  test "renders inside the IDE shell with the library and a selected asset", %{
    conn: conn,
    designer: designer
  } do
    {:ok, asset} = create_tileset(designer, "Forest")
    {:ok, _view, html} = live(conn, ~p"/app/assets")

    assert html =~ "asset-root"
    assert html =~ ~s(id="asset-toolbar")
    assert html =~ ~s(id="asset-#{asset.id}")
  end

  test "upload button opens the modal", %{conn: conn, designer: designer} do
    {:ok, _asset} = create_tileset(designer, "Forest")
    {:ok, view, _html} = live(conn, ~p"/app/assets")

    html = render_click(view, "open_upload")
    assert html =~ ~s(id="upload-modal")
  end

  describe "spritesheet animation editor" do
    setup %{designer: designer} do
      {:ok, sheet} =
        Library.create_spritesheet(designer.id, %{
          name: "hero",
          sha256: :crypto.strong_rand_bytes(32),
          content_url: "http://example.com/hero.png",
          byte_size: 2048,
          mime_type: "image/png",
          width: 128,
          height: 64
        })

      {:ok, sheet: sheet}
    end

    test "creates, edits, and deletes an animation", %{
      conn: conn,
      designer: designer,
      sheet: sheet
    } do
      {:ok, view, _html} = live(conn, ~p"/app/assets")
      render_click(view, "select_asset", %{"id" => sheet.id})

      # Create.
      render_submit(view, "anim_new", %{"animation" => %{"name" => "walk"}})
      sheet = Library.get_asset!(designer.id, sheet.id)

      assert [%{"name" => "walk", "frames" => [], "fps" => 8, "loop" => true}] =
               sheet.metadata["animations"]

      # Build the frame list by clicking frames (order preserved), toggle one off.
      render_click(view, "anim_toggle_frame", %{"frame" => "4"})
      render_click(view, "anim_toggle_frame", %{"frame" => "5"})
      render_click(view, "anim_toggle_frame", %{"frame" => "6"})
      render_click(view, "anim_toggle_frame", %{"frame" => "5"})

      sheet = Library.get_asset!(designer.id, sheet.id)
      assert [%{"frames" => [4, 6]}] = sheet.metadata["animations"]

      # Settings.
      render_change(view, "anim_settings", %{"animation" => %{"fps" => "12", "loop" => "false"}})
      sheet = Library.get_asset!(designer.id, sheet.id)
      assert [%{"fps" => 12, "loop" => false}] = sheet.metadata["animations"]

      # The live preview plays via the Sprite hook.
      assert render(view) =~ ~s(data-sprite-frames="4,6")

      # Delete.
      render_click(view, "anim_delete", %{"name" => "walk"})
      sheet = Library.get_asset!(designer.id, sheet.id)
      assert sheet.metadata["animations"] == []
    end

    test "rejects duplicate animation names", %{conn: conn, designer: designer, sheet: sheet} do
      {:ok, view, _html} = live(conn, ~p"/app/assets")
      render_click(view, "select_asset", %{"id" => sheet.id})

      render_submit(view, "anim_new", %{"animation" => %{"name" => "walk"}})
      render_submit(view, "anim_new", %{"animation" => %{"name" => "walk"}})

      sheet = Library.get_asset!(designer.id, sheet.id)
      assert length(sheet.metadata["animations"]) == 1
    end
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
end
