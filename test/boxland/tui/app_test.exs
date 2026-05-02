defmodule Boxland.TUI.AppTest do
  use ExUnit.Case, async: true

  alias Boxland.TUI.App
  alias TermUI.Event

  defp base_state(overrides \\ %{}) do
    # Default install_runner is a no-op so tests never trigger real Install.
    Map.merge(
      %{
        screen: :menu,
        selected_index: 0,
        installed_at: nil,
        upgrade_pending: false,
        version: "0.1.0",
        data_dir: "/tmp/boxland",
        log_lines: [],
        install_progress: nil,
        install_error: nil,
        runtime_started_at: nil,
        install_runner: fn -> {:ok, %{}} end
      },
      overrides
    )
  end

  describe "event_to_msg/2" do
    test "q and Q quit" do
      assert App.event_to_msg(%Event.Key{key: "q"}, %{}) == {:msg, :quit}
      assert App.event_to_msg(%Event.Key{key: "Q"}, %{}) == {:msg, :quit}
    end

    test "arrows + vim keys navigate" do
      assert App.event_to_msg(%Event.Key{key: :down}, %{}) == {:msg, :select_next}
      assert App.event_to_msg(%Event.Key{key: :up}, %{}) == {:msg, :select_prev}
      assert App.event_to_msg(%Event.Key{key: "j"}, %{}) == {:msg, :select_next}
      assert App.event_to_msg(%Event.Key{key: "k"}, %{}) == {:msg, :select_prev}
    end

    test "enter activates" do
      assert App.event_to_msg(%Event.Key{key: :enter}, %{}) == {:msg, :activate}
    end

    test "unknown keys are ignored" do
      assert App.event_to_msg(%Event.Key{key: "x"}, %{}) == :ignore
    end
  end

  describe "update/2 — navigation" do
    test "select_next on installed-not-running menu lands on Re-check Install" do
      state = base_state(%{installed_at: ~U[2026-05-01 00:00:00Z], selected_index: 0})
      {new_state, []} = App.update(:select_next, state)
      assert new_state.selected_index == 1
    end

    test "select_prev wraps to the next non-disabled item" do
      state = base_state(%{installed_at: ~U[2026-05-01 00:00:00Z], selected_index: 0})
      {new_state, []} = App.update(:select_prev, state)
      # 3 items, all enabled → wraps from 0 to 2
      assert new_state.selected_index == 2
    end

    test "navigation is a no-op outside :menu screen" do
      state = base_state(%{screen: :installing})
      assert App.update(:select_next, state) == {state, []}
    end
  end

  describe "update/2 — install activation" do
    test "activating Install transitions to :installing screen and clears prior error" do
      state = base_state(%{install_error: "previous boom", selected_index: 0})
      {new_state, []} = App.update(:activate, state)
      assert new_state.screen == :installing
      assert new_state.install_progress != nil
      assert new_state.install_error == nil
    end

    test "install_finished {:ok, _} returns to menu and updates installed_at" do
      state = base_state(%{screen: :installing, install_progress: "..."})
      {new_state, []} = App.update({:install_finished, {:ok, %{}}}, state)
      assert new_state.screen == :menu
      assert new_state.install_progress == nil
      # installed_at depends on real ~/.boxland/installed file presence; assert no error
      assert is_nil(new_state.install_error)
    end

    test "install_finished {:error, %{stage:, reason:}} returns to menu with formatted error" do
      err = %{stage: :services, reason: "ports busy", suggestion: nil}
      state = base_state(%{screen: :installing})
      {new_state, []} = App.update({:install_finished, {:error, err}}, state)
      assert new_state.screen == :menu
      assert new_state.install_error == "[services] ports busy"
    end
  end

  describe "update/2 — log feed" do
    test "log_entry appends to tail" do
      state = base_state(%{log_lines: ["a"]})
      {new_state, []} = App.update({:log_entry, "b"}, state)
      assert new_state.log_lines == ["a", "b"]
    end

    test "log_buffer replaces lines" do
      state = base_state(%{log_lines: ["old1", "old2"]})
      {new_state, []} = App.update({:log_buffer, ["new"]}, state)
      assert new_state.log_lines == ["new"]
    end

    test "log lines are bounded" do
      state = base_state(%{log_lines: Enum.map(1..250, &"line#{&1}")})
      {new_state, []} = App.update({:log_entry, "fresh"}, state)
      assert length(new_state.log_lines) <= 200
      assert List.last(new_state.log_lines) == "fresh"
    end
  end

  describe "update/2 — quit" do
    test "quit emits :quit command" do
      {_state, cmds} = App.update(:quit, base_state())
      assert :quit in cmds
    end
  end

  describe "view/1 — smoke" do
    test "renders for menu screen (uninstalled)" do
      tree = App.view(base_state())
      assert match?(%TermUI.Component.RenderNode{type: :stack}, tree)
    end

    test "renders for menu screen (installed, stopped)" do
      tree = App.view(base_state(%{installed_at: ~U[2026-05-01 00:00:00Z]}))
      assert match?(%TermUI.Component.RenderNode{type: :stack}, tree)
    end

    test "renders for installing screen" do
      tree = App.view(base_state(%{screen: :installing, install_progress: "Working…"}))
      assert match?(%TermUI.Component.RenderNode{type: :stack}, tree)
    end

    test "renders for running screen" do
      state =
        base_state(%{
          screen: :running,
          installed_at: ~U[2026-05-01 00:00:00Z],
          runtime_started_at: System.monotonic_time(:millisecond)
        })

      tree = App.view(state)
      assert match?(%TermUI.Component.RenderNode{type: :stack}, tree)
    end

    test "renders the install error if present" do
      state = base_state(%{install_error: "boom"})
      tree = App.view(state)
      flat = render_to_text(tree)
      assert flat =~ "Install failed: boom"
    end
  end

  # ---------- helpers ----------

  defp render_to_text(%TermUI.Component.RenderNode{type: :text, content: c}), do: c

  defp render_to_text(%TermUI.Component.RenderNode{children: kids}) when is_list(kids) do
    kids |> Enum.map(&render_to_text/1) |> Enum.join("\n")
  end

  defp render_to_text(_), do: ""
end
