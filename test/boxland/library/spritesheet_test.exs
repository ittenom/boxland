defmodule Boxland.Library.SpritesheetTest do
  use Boxland.DataCase, async: true

  alias Boxland.Library

  setup do
    {:ok, designer} =
      %Boxland.Auth.Designer{}
      |> Boxland.Auth.Designer.changeset(%{
        email: "sheet@e.com",
        password_hash: "x",
        display_name: "S"
      })
      |> Boxland.Repo.insert()

    {:ok, designer: designer}
  end

  defp sheet_attrs(overrides \\ %{}) do
    Map.merge(
      %{
        name: "hero",
        sha256: :crypto.hash(:sha256, "sheet-#{System.unique_integer()}"),
        content_url: "https://cdn.example.com/sheet.png",
        byte_size: 4096,
        mime_type: "image/png",
        width: 128,
        height: 64
      },
      overrides
    )
  end

  describe "create_spritesheet/2" do
    test "builds the spritesheet metadata shape", %{designer: d} do
      assert {:ok, asset} = Library.create_spritesheet(d.id, sheet_attrs())

      assert asset.kind == "spritesheet"

      assert asset.metadata == %{
               "tile_size" => 32,
               "width" => 128,
               "height" => 64,
               "grid_rows" => 2,
               "grid_cols" => 4,
               "frame_count" => 8,
               "frame_indexes" => [0, 1, 2, 3, 4, 5, 6, 7],
               "animations" => []
             }
    end

    test "honors precomputed alpha-visible frame indexes", %{designer: d} do
      attrs = sheet_attrs(%{frame_indexes: [0, 2, 5]})
      assert {:ok, asset} = Library.create_spritesheet(d.id, attrs)
      assert asset.metadata["frame_indexes"] == [0, 2, 5]
    end

    test "rejects dimensions not divisible by 32", %{designer: d} do
      assert {:error, message} = Library.create_spritesheet(d.id, sheet_attrs(%{width: 100}))
      assert message =~ "divisible by 32"
    end
  end

  describe "put_animations/2 and animation/2" do
    setup %{designer: d} do
      {:ok, asset} = Library.create_spritesheet(d.id, sheet_attrs())
      {:ok, asset: asset}
    end

    test "stores and reads back valid animations", %{asset: asset} do
      animations = [
        %{"name" => "idle", "frames" => [0, 1, 2, 3], "fps" => 8, "loop" => true},
        %{"name" => "walk", "frames" => [4, 5, 6, 7], "fps" => 12, "loop" => true}
      ]

      assert {:ok, updated} = Library.put_animations(asset, animations)
      assert updated.metadata["animations"] == animations
      assert Library.animation(updated, "walk")["fps"] == 12
      assert Library.animation(updated, "missing") == nil
    end

    test "allows empty frames mid-edit", %{asset: asset} do
      assert {:ok, _} =
               Library.put_animations(asset, [
                 %{"name" => "wip", "frames" => [], "fps" => 8, "loop" => true}
               ])
    end

    test "rejects out-of-range frames", %{asset: asset} do
      assert {:error, _} =
               Library.put_animations(asset, [
                 %{"name" => "bad", "frames" => [0, 99], "fps" => 8, "loop" => true}
               ])
    end

    test "rejects bad fps and duplicate names", %{asset: asset} do
      assert {:error, _} =
               Library.put_animations(asset, [
                 %{"name" => "fast", "frames" => [0], "fps" => 600, "loop" => true}
               ])

      assert {:error, "animation names must be unique"} =
               Library.put_animations(asset, [
                 %{"name" => "a", "frames" => [0], "fps" => 8, "loop" => true},
                 %{"name" => "a", "frames" => [1], "fps" => 8, "loop" => false}
               ])
    end
  end
end
