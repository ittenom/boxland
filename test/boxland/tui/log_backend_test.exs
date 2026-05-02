defmodule Boxland.TUI.LogBackendTest do
  # shared GenServer
  use ExUnit.Case, async: false

  alias Boxland.TUI.LogBackend

  setup do
    # Restart with small buffer for predictable testing
    if Process.whereis(LogBackend), do: GenServer.stop(LogBackend)
    {:ok, _pid} = LogBackend.start_link(buffer_size: 4)
    :ok
  end

  test "format/3 produces HH:MM:SS.mmm [level] message" do
    formatted = LogBackend.format(:info, "hello world", %{time: ~U[2026-05-01 23:45:12.834000Z]})
    assert formatted =~ "23:45:12.834"
    assert formatted =~ "[info]"
    assert formatted =~ "hello world"
  end

  test "log/2 pushes to ring buffer up to capacity then drops oldest" do
    LogBackend.log(:info, "one")
    LogBackend.log(:info, "two")
    LogBackend.log(:info, "three")
    LogBackend.log(:info, "four")
    LogBackend.log(:info, "five")
    buf = LogBackend.buffer()
    assert length(buf) == 4
    assert hd(buf) =~ "two"
    assert List.last(buf) =~ "five"
  end

  test "subscribe/1 receives existing buffer + new entries" do
    LogBackend.log(:info, "before")
    LogBackend.subscribe(self())
    assert_receive {:log_buffer, entries}, 100
    assert length(entries) == 1
    assert hd(entries) =~ "before"

    LogBackend.log(:info, "after")
    assert_receive {:log_entry, line}, 100
    assert line =~ "after"
  end

  test "unsubscribe/1 stops delivery" do
    LogBackend.subscribe(self())
    assert_receive {:log_buffer, _}, 100
    LogBackend.unsubscribe(self())
    LogBackend.log(:info, "ignored")
    refute_receive {:log_entry, _}, 50
  end

  test "subscriber pid death auto-cleans subscription" do
    {pid, ref} =
      spawn_monitor(fn ->
        LogBackend.subscribe(self())

        receive do
          :stop -> :ok
        after
          100 -> :ok
        end
      end)

    # let subscribe complete
    Process.sleep(20)
    Process.exit(pid, :kill)
    assert_receive {:DOWN, ^ref, :process, ^pid, _}, 200

    # The LogBackend's subscribers map should now have 0 entries.
    # allow LogBackend to handle the :DOWN
    Process.sleep(20)
    assert LogBackend.subscriber_count() == 0
  end

  test "flush/0 empties the ring buffer" do
    LogBackend.log(:info, "one")
    LogBackend.log(:info, "two")
    assert length(LogBackend.buffer()) == 2
    LogBackend.flush()
    assert LogBackend.buffer() == []
  end
end
