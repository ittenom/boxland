defmodule Boxland.TUI.ServerRuntimeTest do
  use ExUnit.Case, async: false
  alias Boxland.TUI.ServerRuntime

  setup do
    Boxland.Server.Supervisor.stop_children()
    on_exit(fn -> Boxland.Server.Supervisor.stop_children() end)
    :ok
  end

  test "start/0 brings up Phoenix children" do
    assert ServerRuntime.status() == :stopped
    assert :ok = ServerRuntime.start()
    assert ServerRuntime.status() == :running
  end

  test "stop/0 tears down children" do
    :ok = ServerRuntime.start()
    assert :ok = ServerRuntime.stop()
    assert ServerRuntime.status() == :stopped
  end

  test "elapsed/1 reports elapsed seconds since start" do
    start_time = System.monotonic_time(:millisecond)
    assert ServerRuntime.elapsed(start_time) >= 0
    Process.sleep(50)
    assert ServerRuntime.elapsed(start_time) >= 50
  end

  test "format_elapsed/1 produces M:SS" do
    assert ServerRuntime.format_elapsed(0) == "0:00"
    assert ServerRuntime.format_elapsed(32_000) == "0:32"
    assert ServerRuntime.format_elapsed(125_000) == "2:05"
    assert ServerRuntime.format_elapsed(3_605_000) == "60:05"
  end
end
