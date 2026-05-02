defmodule Boxland.TUI.ThemeTest do
  use ExUnit.Case, async: true
  alias Boxland.TUI.Theme

  test "colors/0 returns a map with all required tokens" do
    c = Theme.colors()
    for token <- ~w(accent_warm accent_warm_end accent_cool success warning error text text_muted text_subtle border)a do
      assert Map.has_key?(c, token), "missing color token: #{token}"
      {r, g, b} = c[token]
      assert is_integer(r) and r in 0..255
      assert is_integer(g) and g in 0..255
      assert is_integer(b) and b in 0..255
    end
  end

  test "logo_full/0 returns 6 lines of equal width" do
    lines = Theme.logo_full() |> String.split("\n", trim: true)
    assert length(lines) == 6
    widths = Enum.map(lines, &String.length/1)
    [w | rest] = widths
    assert Enum.all?(rest, &(&1 == w)), "logo lines have unequal widths: #{inspect(widths)}"
  end

  test "logo_compact/0 returns 2 lines" do
    lines = Theme.logo_compact() |> String.split("\n", trim: true)
    assert length(lines) == 2
  end

  test "lerp_color/3 interpolates RGB linearly" do
    a = {0, 0, 0}
    b = {200, 100, 50}
    assert Theme.lerp_color(a, b, 0.0) == {0, 0, 0}
    assert Theme.lerp_color(a, b, 1.0) == {200, 100, 50}
    assert Theme.lerp_color(a, b, 0.5) == {100, 50, 25}
  end

  test "gradient_for_column/3 picks a color from accent_warm to accent_warm_end" do
    c = Theme.colors()
    leftmost = Theme.gradient_for_column(0, 100, c)
    rightmost = Theme.gradient_for_column(99, 100, c)
    assert leftmost == c.accent_warm
    {r, _, _} = rightmost
    {r_end, _, _} = c.accent_warm_end
    assert abs(r - r_end) < 10
  end
end
