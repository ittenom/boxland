defmodule Boxland.TUI.Views.RuntimeViewTest do
  use ExUnit.Case, async: true
  alias Boxland.TUI.Views.RuntimeView

  test "render/1 returns logo strip + status + log pane + footer" do
    state = %{
      url: "http://localhost:4000",
      elapsed_ms: 32_000,
      log_lines: ["23:45:12.834 [info]  hello"],
      status: :running
    }
    tree = RuntimeView.render(state)
    flat = RuntimeView.flatten_for_test(tree)
    assert Enum.any?(flat, &String.contains?(&1, "Server running 0:32"))
    assert Enum.any?(flat, &String.contains?(&1, "http://localhost:4000"))
    assert Enum.any?(flat, &String.contains?(&1, "hello"))
    assert Enum.any?(flat, &String.contains?(&1, "Esc"))
  end

  test "stopping state shows 'Stopping'" do
    state = %{
      url: "http://localhost:4000",
      elapsed_ms: 32_000,
      log_lines: [],
      status: :stopping
    }
    tree = RuntimeView.render(state)
    flat = RuntimeView.flatten_for_test(tree)
    assert Enum.any?(flat, &String.contains?(&1, "Stopping"))
  end
end
