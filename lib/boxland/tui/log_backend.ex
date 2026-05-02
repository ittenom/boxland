defmodule Boxland.TUI.LogBackend do
  @moduledoc """
  In-process log capture for the TUI runtime view.

  Uses the modern `:logger` handler API (OTP 21+). Captures every log
  event into a size-bounded ring buffer (FIFO eviction) and broadcasts
  to subscribed processes. Subscribers receive the existing buffer on
  subscribe, then individual `{:log_entry, formatted_line}` messages
  for each new entry.
  """

  use GenServer

  defstruct buffer: :queue.new(), buffer_size: 5000, buffer_count: 0, subscribers: %{}

  # Public API

  def start_link(opts \\ []) do
    GenServer.start_link(__MODULE__, opts, name: __MODULE__)
  end

  @doc "Format a log event into the canonical `HH:MM:SS.mmm [level] message` string."
  def format(level, message, metadata \\ %{}) do
    time = Map.get(metadata, :time, DateTime.utc_now())
    {h, m, s, ms} = {time.hour, time.minute, time.second, div(elem(time.microsecond, 0), 1000)}
    Enum.join([
      :io_lib.format("~2..0B:~2..0B:~2..0B.~3..0B", [h, m, s, ms]) |> IO.iodata_to_binary(),
      " [", to_string(level), "] ",
      message
    ])
  end

  @doc "Push a log entry. Used by the :logger handler and direct callers (tests)."
  def log(level, message, metadata \\ %{}) do
    GenServer.cast(__MODULE__, {:log, level, message, metadata})
  end

  @doc "Subscribe a pid for new log entries. Sends `{:log_buffer, entries}` immediately."
  def subscribe(pid) when is_pid(pid) do
    GenServer.call(__MODULE__, {:subscribe, pid})
  end

  @doc "Unsubscribe a pid. Idempotent."
  def unsubscribe(pid) when is_pid(pid) do
    GenServer.call(__MODULE__, {:unsubscribe, pid})
  end

  @doc "Read the current buffer as a list of formatted lines (oldest first)."
  def buffer do
    GenServer.call(__MODULE__, :buffer)
  end

  @doc "Empty the buffer."
  def flush do
    GenServer.call(__MODULE__, :flush)
  end

  @doc "How many subscribers are currently registered."
  def subscriber_count do
    GenServer.call(__MODULE__, :subscriber_count)
  end

  # GenServer callbacks

  @impl true
  def init(opts) do
    buffer_size = Keyword.get(opts, :buffer_size, 5000)
    {:ok, %__MODULE__{buffer_size: buffer_size}}
  end

  @impl true
  def handle_cast({:log, level, message, metadata}, state) do
    formatted = format(level, message, metadata)

    {buffer, count} =
      if state.buffer_count >= state.buffer_size do
        {{_, b}, _} = {:queue.out(state.buffer), state.buffer_count}
        {:queue.in(formatted, b), state.buffer_count}
      else
        {:queue.in(formatted, state.buffer), state.buffer_count + 1}
      end

    Enum.each(state.subscribers, fn {pid, _ref} ->
      send(pid, {:log_entry, formatted})
    end)

    {:noreply, %{state | buffer: buffer, buffer_count: count}}
  end

  @impl true
  def handle_call({:subscribe, pid}, _from, state) do
    state =
      case Map.fetch(state.subscribers, pid) do
        {:ok, _} ->
          state

        :error ->
          ref = Process.monitor(pid)
          send(pid, {:log_buffer, :queue.to_list(state.buffer)})
          %{state | subscribers: Map.put(state.subscribers, pid, ref)}
      end

    {:reply, :ok, state}
  end

  @impl true
  def handle_call({:unsubscribe, pid}, _from, state) do
    state =
      case Map.fetch(state.subscribers, pid) do
        {:ok, ref} ->
          Process.demonitor(ref, [:flush])
          %{state | subscribers: Map.delete(state.subscribers, pid)}

        :error ->
          state
      end

    {:reply, :ok, state}
  end

  @impl true
  def handle_call(:buffer, _from, state) do
    {:reply, :queue.to_list(state.buffer), state}
  end

  @impl true
  def handle_call(:flush, _from, state) do
    {:reply, :ok, %{state | buffer: :queue.new(), buffer_count: 0}}
  end

  @impl true
  def handle_call(:subscriber_count, _from, state) do
    {:reply, map_size(state.subscribers), state}
  end

  @impl true
  def handle_info({:DOWN, _ref, :process, pid, _reason}, state) do
    {:noreply, %{state | subscribers: Map.delete(state.subscribers, pid)}}
  end
end
