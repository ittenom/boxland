defmodule BoxlandWeb.LevelRenderTest do
  use ExUnit.Case, async: true

  alias BoxlandWeb.LevelRender

  @sheet %{
    id: 7,
    kind: "spritesheet",
    content_url: "https://cdn.example.com/sheet.png",
    metadata: %{
      "tile_size" => 32,
      "grid_cols" => 4,
      "grid_rows" => 2,
      "frame_count" => 8,
      "animations" => [
        %{"name" => "idle", "frames" => [0, 1], "fps" => 8, "loop" => true},
        %{"name" => "walk", "frames" => [4, 5, 6, 7], "fps" => 12, "loop" => true},
        %{"name" => "empty", "frames" => [], "fps" => 8, "loop" => true}
      ]
    }
  }

  describe "animation_data/2" do
    test "returns playback data for a named animation" do
      assert %{
               url: "https://cdn.example.com/sheet.png",
               cols: 4,
               rows: 2,
               frames: [4, 5, 6, 7],
               fps: 12,
               loop: true
             } = LevelRender.animation_data(@sheet, "walk")
    end

    test "accepts string-keyed snapshot asset maps" do
      snapshot_asset = %{
        "content_url" => @sheet.content_url,
        "metadata" => @sheet.metadata
      }

      assert %{frames: [0, 1]} = LevelRender.animation_data(snapshot_asset, "idle")
    end

    test "nil for missing assets, missing animations, and empty frame lists" do
      assert LevelRender.animation_data(nil, "idle") == nil
      assert LevelRender.animation_data(@sheet, "nope") == nil
      assert LevelRender.animation_data(@sheet, "empty") == nil
    end
  end

  describe "sprite_attrs/2" do
    test "ambient attributes" do
      attrs = LevelRender.sprite_attrs(LevelRender.animation_data(@sheet, "idle"))

      assert {"phx-hook", "Sprite"} in attrs
      assert {"data-sprite-frames", "0,1"} in attrs
      assert {"data-sprite-sync", "ambient"} in attrs
      refute List.keymember?(attrs, "data-sprite-tick", 0)
    end

    test "tick-synced attributes carry the sim tick" do
      attrs =
        LevelRender.sprite_attrs(LevelRender.animation_data(@sheet, "walk"),
          sync: "tick",
          tick: 42
        )

      assert {"data-sprite-sync", "tick"} in attrs
      assert {"data-sprite-tick", 42} in attrs
      assert {"data-sprite-ticks-per-frame", 2} in attrs
    end

    test "nil data yields no attributes" do
      assert LevelRender.sprite_attrs(nil) == []
    end
  end

  describe "resolved_animation/2" do
    test "binding for the state wins, then default, then visual_ref animation" do
      type = %{
        animation_bindings: %{"moving" => "walk", "default" => "idle"},
        visual_ref: %{"kind" => "animated", "animation" => "fallback"}
      }

      assert LevelRender.resolved_animation(type, "moving") == "walk"
      assert LevelRender.resolved_animation(type, "idle") == "idle"

      no_default = %{type | animation_bindings: %{"moving" => "walk"}}
      assert LevelRender.resolved_animation(no_default, "idle") == "fallback"

      string_keyed = %{
        "animation_bindings" => %{"moving" => "walk"},
        "visual_ref" => %{"animation" => "fallback"}
      }

      assert LevelRender.resolved_animation(string_keyed, "moving") == "walk"
    end
  end

  describe "transform_style/1" do
    test "identity transforms render nothing" do
      assert LevelRender.transform_style(nil) == ""
      assert LevelRender.transform_style(%{}) == ""

      assert LevelRender.transform_style(%{
               "mirror_x" => false,
               "mirror_y" => false,
               "rotation" => 0,
               "scale" => 1.0
             }) == ""
    end

    test "mirror folds into negative scale" do
      assert LevelRender.transform_style(%{"mirror_x" => true}) =~ "scale(-1.0, 1.0)"
      assert LevelRender.transform_style(%{"mirror_y" => true}) =~ "scale(1.0, -1.0)"
    end

    test "composes scale, mirror, and rotation about the center" do
      style =
        LevelRender.transform_style(%{
          "mirror_x" => true,
          "rotation" => 90,
          "scale" => 2.0
        })

      assert style == "transform: scale(-2.0, 2.0) rotate(90deg); transform-origin: center;"
    end
  end

  describe "animated cells" do
    test "animated_cell?/1 discriminates on the kind key" do
      assert LevelRender.animated_cell?(%{"kind" => "animated", "animation" => "idle"})
      refute LevelRender.animated_cell?(%{"asset_id" => 1, "tile_index" => 0})
      refute LevelRender.animated_cell?(nil)
    end

    test "tile_anim_data/2 resolves through the assets map" do
      cell = %{"asset_id" => 7, "tile_index" => 0, "animation" => "idle", "kind" => "animated"}
      assert %{frames: [0, 1]} = LevelRender.tile_anim_data(cell, %{7 => @sheet})
      assert LevelRender.tile_anim_data(cell, %{}) == nil
    end
  end

  describe "first_animation_frame/2" do
    test "first frame of the animation, defaulting to 0" do
      assert LevelRender.first_animation_frame(@sheet, "walk") == 4
      assert LevelRender.first_animation_frame(@sheet, "missing") == 0
    end
  end
end
