defmodule Boxland.TUI.Server do
  @moduledoc """
  Top-level TUI process. Owns the term_ui application loop, holds menu
  state, and dispatches to Install / ServerRuntime based on user input.

  This module is a thin GenServer wrapper. The view tree and update logic
  live in `Boxland.TUI.Views.MenuView` / `Boxland.TUI.Views.RuntimeView`.
  """

  use GenServer

  alias Boxland.TUI.{Install, Menu}

  defmodule State do
    defstruct [
      :term_ui_pid,
      :selected_index,
      :installed_at,
      :upgrade_pending,
      :screen,
      :install_progress,
      :runtime_started_at
    ]
  end

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @impl true
  def init(_opts) do
    marker = Install.read_marker()
    installed_at = if marker, do: marker.installed_at, else: nil
    current_version = to_string(Application.spec(:boxland, :vsn))
    upgrade = Menu.upgrade_pending?(marker && marker.version, current_version)

    state = %State{
      selected_index: 0,
      installed_at: installed_at,
      upgrade_pending: upgrade,
      screen: :menu,
      install_progress: nil,
      runtime_started_at: nil
    }

    # term_ui startup goes here. Stub for now — actual call shape
    # depends on term_ui v1 API (verify via mix.exs install).
    {:ok, state}
  end
end
