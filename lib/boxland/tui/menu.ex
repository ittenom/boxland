defmodule Boxland.TUI.Menu do
  @moduledoc """
  Menu state machine. Pure: takes installed_at + server_status,
  returns the ordered, styled list of items to show.

  Menu items have shape:
    %{id: atom, label: String.t, description: String.t,
      glyph: String.t, style: :featured | :normal | :demoted | :muted,
      disabled?: boolean}
  """

  @typedoc "Menu item descriptor."
  @type item :: %{
          id: atom(),
          label: String.t(),
          description: String.t(),
          glyph: String.t(),
          style: :featured | :normal | :demoted | :muted,
          disabled?: boolean()
        }

  @doc """
  Returns the ordered list of menu items for the given state.

  Options:
    * :installed_at — DateTime or nil
    * :server_status — :stopped | :running
    * :upgrade_pending — boolean (default false)
  """
  def items(opts) do
    installed = Keyword.get(opts, :installed_at) != nil
    running = Keyword.get(opts, :server_status, :stopped) == :running
    upgrade = Keyword.get(opts, :upgrade_pending, false)

    cond do
      not installed -> pre_install_items()
      running -> running_items()
      upgrade -> upgrade_pending_items()
      true -> stopped_items()
    end
  end

  defp pre_install_items do
    [
      %{id: :install, label: "Install", description: "Install dependencies and set up Boxland.",
        glyph: "★", style: :featured, disabled?: false},
      %{id: :run_server, label: "Run Server", description: "Install must complete first.",
        glyph: "▶", style: :muted, disabled?: true},
      %{id: :quit, label: "Quit", description: "Exit Boxland.",
        glyph: "⏻", style: :muted, disabled?: false}
    ]
  end

  defp stopped_items do
    [
      %{id: :run_server, label: "Run Server", description: "Phoenix on :4000",
        glyph: "▶", style: :featured, disabled?: false},
      %{id: :recheck_install, label: "Re-check Install", description: "Verify deps + heal.",
        glyph: "⟲", style: :demoted, disabled?: false},
      %{id: :quit, label: "Quit", description: "Exit Boxland.",
        glyph: "⏻", style: :muted, disabled?: false}
    ]
  end

  defp running_items do
    [
      %{id: :stop_server, label: "Stop Server", description: "Stop Phoenix gracefully.",
        glyph: "■", style: :featured, disabled?: false},
      %{id: :recheck_install, label: "Re-check Install", description: "Stop server first.",
        glyph: "⟲", style: :muted, disabled?: true},
      %{id: :quit, label: "Quit", description: "Stop server and exit.",
        glyph: "⏻", style: :muted, disabled?: false}
    ]
  end

  defp upgrade_pending_items do
    [
      %{id: :recheck_install, label: "Re-check Install", description: "Upgrade detected — recommended.",
        glyph: "⟲", style: :featured, disabled?: false},
      %{id: :run_server, label: "Run Server", description: "Phoenix on :4000",
        glyph: "▶", style: :demoted, disabled?: false},
      %{id: :quit, label: "Quit", description: "Exit Boxland.",
        glyph: "⏻", style: :muted, disabled?: false}
    ]
  end

  @doc "Find the next selectable index given a current position and direction."
  def next_selectable(items, current_idx, direction) when direction in [:up, :down] do
    n = length(items)
    step = if direction == :down, do: 1, else: -1

    Stream.iterate(current_idx, &rem(&1 + step + n, n))
    |> Stream.drop(1)   # don't return current
    |> Enum.find(fn i -> not Enum.at(items, i).disabled? end)
  end

  @doc """
  Compare a marker version (from ~/.boxland/installed) against the current
  binary version. Returns true if they differ (and marker is non-nil).
  """
  def upgrade_pending?(marker_version, current_version)
  def upgrade_pending?(nil, _), do: false
  def upgrade_pending?(marker, current), do: marker != current
end
