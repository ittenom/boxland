defmodule Boxland.TUI.Theme do
  @moduledoc """
  Color palette and logo data for the Boxland TUI.

  All colors are RGB tuples `{r, g, b}` with integer components 0..255.
  Term_ui style structs reference these by token. We don't paint
  backgrounds — the terminal's native bg theme stays.
  """

  @doc "Named color tokens used throughout TUI views."
  def colors do
    %{
      accent_warm:     {0xff, 0x9e, 0xc7},  # soft pink
      accent_warm_end: {0xff, 0xb8, 0x6b},  # warm orange (logo gradient endpoint)
      accent_cool:     {0x5c, 0xcf, 0xe6},  # cool blue (status indicators)
      success:         {0x3d, 0xd9, 0x7c},  # green
      warning:         {0xf4, 0xc4, 0x30},  # amber
      error:           {0xf0, 0x68, 0x70},  # rose-red
      text:            {0xe6, 0xe6, 0xe6},  # near-white
      text_muted:      {0x99, 0x99, 0x99},  # gray60
      text_subtle:     {0x66, 0x66, 0x66},  # gray40
      border:          {0x66, 0x66, 0x66}   # gray40
    }
  end

  @doc "Linearly interpolate two RGB colors. Factor 0.0 = a, 1.0 = b."
  def lerp_color({r1, g1, b1}, {r2, g2, b2}, factor) when factor >= 0.0 and factor <= 1.0 do
    {
      round(r1 + (r2 - r1) * factor),
      round(g1 + (g2 - g1) * factor),
      round(b1 + (b2 - b1) * factor)
    }
  end

  @doc "Gradient color for column `col` of `total_cols` (warm -> warm_end)."
  def gradient_for_column(col, total_cols, colors \\ colors()) do
    factor = if total_cols <= 1, do: 0.0, else: col / (total_cols - 1)
    lerp_color(colors.accent_warm, colors.accent_warm_end, factor)
  end

  @doc "6-line BOXLAND ASCII logo for the menu screen. Generated via figlet 'ANSI Shadow'."
  def logo_full do
    [
      "██████╗  ██████╗ ██╗  ██╗██╗      █████╗ ███╗   ██╗██████╗ ",
      "██╔══██╗██╔═══██╗ ██╗██╔╝██║     ██╔══██╗████╗  ██║██╔══██╗",
      "██████╔╝██║   ██║  ███╔╝ ██║     ███████║██╔██╗ ██║██║  ██║",
      "██╔══██╗██║   ██║██╔██╗  ██║     ██╔══██║██║╚██╗██║██║  ██║",
      "██████╔╝╚██████╔╝██║ ██╗ ███████╗██║  ██║██║ ╚████║██████╔╝",
      "╚═════╝  ╚═════╝ ╚═╝ ╚═╝ ╚══════╝╚═╝  ╚═╝╚═╝  ╚═══╝╚═════╝ "
    ]
    |> normalize_width()
    |> Enum.join("\n")
  end

  @doc "2-line compact BOXLAND for the runtime view's strip."
  def logo_compact do
    [
      "▄▄▄▄  ▄▄▄▄ ▄   ▄ ▄     ▄▄▄  ▄   ▄ ▄▄▄▄ ",
      "█▄▄█  █  █  ▀▄▀  █     █▄▄█ █▄▀▀▄ █  █ "
    ]
    |> Enum.join("\n")
  end

  # Pads all lines to the same width using the longest line as reference.
  defp normalize_width(lines) do
    max_len = Enum.map(lines, &String.length/1) |> Enum.max()
    Enum.map(lines, fn line ->
      padding = max_len - String.length(line)
      line <> String.duplicate(" ", padding)
    end)
  end
end
