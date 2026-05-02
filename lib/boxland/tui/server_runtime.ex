defmodule Boxland.TUI.ServerRuntime do
  @moduledoc """
  Thin wrapper around `Boxland.Server.Supervisor` for the runtime view.
  Provides start/stop/status, an elapsed-time helper, and a formatter.
  """

  alias Boxland.Server.Supervisor, as: Sup

  @spec start() :: :ok
  def start, do: Sup.start_children()

  @spec stop() :: :ok
  def stop, do: Sup.stop_children()

  @spec status() :: :stopped | :running
  def status, do: Sup.status()

  @spec started_at() :: integer()
  def started_at, do: System.monotonic_time(:millisecond)

  @spec elapsed(integer()) :: non_neg_integer()
  def elapsed(start_ms) do
    max(0, System.monotonic_time(:millisecond) - start_ms)
  end

  @spec format_elapsed(non_neg_integer()) :: String.t()
  def format_elapsed(elapsed_ms) do
    total_seconds = div(elapsed_ms, 1000)
    minutes = div(total_seconds, 60)
    seconds = rem(total_seconds, 60)
    "#{minutes}:#{String.pad_leading(Integer.to_string(seconds), 2, "0")}"
  end
end
