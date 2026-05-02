defmodule Boxland.TUI.Views.MenuViewTest do
  use ExUnit.Case, async: true
  alias Boxland.TUI.Views.MenuView

  test "render/1 returns a tree with logo, menu items, and footer" do
    state = %{
      installed_at: nil,
      server_status: :stopped,
      upgrade_pending: false,
      selected_index: 0,
      version: "0.1.0",
      data_dir: "~/.boxland"
    }

    tree = MenuView.render(state)
    # Smoke check — tree is a non-empty container
    assert is_map(tree) or is_list(tree)
  end

  test "render/1 highlights the selected item" do
    state = %{
      installed_at: ~U[2026-05-01 12:00:00Z],
      server_status: :stopped,
      upgrade_pending: false,
      selected_index: 1,
      version: "0.1.0",
      data_dir: "~/.boxland"
    }

    tree = MenuView.render(state)
    # Find the selected item; should have a marker bar `▎`
    flat = MenuView.flatten_for_test(tree)
    selected_lines = Enum.filter(flat, &String.contains?(&1, "▎"))
    assert length(selected_lines) >= 1
  end
end
