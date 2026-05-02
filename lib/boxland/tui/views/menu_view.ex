defmodule Boxland.TUI.Views.MenuView do
  @moduledoc """
  Renders the menu screen as a term_ui widget tree.

  Term_ui v1 API may shape this differently — adjust if needed but
  preserve the visual structure: full logo top, menu items center,
  info strip + key hints footer.
  """

  alias Boxland.TUI.{Menu, Theme}

  @doc """
  Build a renderable representation of the menu screen.

  Expected `state` keys:
    - :installed_at — DateTime or nil
    - :server_status — :stopped | :running
    - :upgrade_pending — boolean
    - :selected_index — integer
    - :version — String.t (current binary version)
    - :data_dir — String.t (~/.boxland or wherever)
  """
  def render(state) do
    items =
      Menu.items(
        installed_at: state.installed_at,
        server_status: state.server_status,
        upgrade_pending: state.upgrade_pending
      )

    %{
      type: :container,
      children: [
        logo_section(),
        items_section(items, state.selected_index),
        info_strip(state),
        key_hints()
      ]
    }
  end

  defp logo_section do
    %{
      type: :logo,
      content: Theme.logo_full(),
      gradient: {Theme.colors().accent_warm, Theme.colors().accent_warm_end}
    }
  end

  defp items_section(items, selected_index) do
    %{
      type: :menu,
      items:
        items
        |> Enum.with_index()
        |> Enum.map(fn {item, idx} ->
          %{
            id: item.id,
            label: item.label,
            description: item.description,
            glyph: item.glyph,
            style: item.style,
            disabled?: item.disabled?,
            selected?: idx == selected_index,
            marker: if(idx == selected_index, do: "▎", else: " ")
          }
        end)
    }
  end

  defp info_strip(state) do
    installed_str =
      case state.installed_at do
        nil -> "not installed"
        dt -> "installed " <> Calendar.strftime(dt, "%Y-%m-%d")
      end

    %{
      type: :info_strip,
      content: "v#{state.version} · #{state.data_dir} · #{installed_str}"
    }
  end

  defp key_hints do
    %{
      type: :key_hints,
      content: "[↑↓ j/k] Navigate   [Enter] Select   [Q] Quit"
    }
  end

  @doc """
  Test helper: walks the tree and produces a flat list of strings,
  including selection markers, so tests can assert on rendered content.
  """
  def flatten_for_test(tree) when is_map(tree) do
    case tree do
      %{type: :container, children: kids} ->
        Enum.flat_map(kids, &flatten_for_test/1)

      %{type: :logo, content: c} ->
        String.split(c, "\n")

      %{type: :menu, items: items} ->
        Enum.map(items, fn i -> "#{i.marker} #{i.glyph} #{i.label}" end)

      %{type: :info_strip, content: c} ->
        [c]

      %{type: :key_hints, content: c} ->
        [c]

      _ ->
        []
    end
  end
end
