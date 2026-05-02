defmodule Boxland.Server.SupervisorTest do
  use ExUnit.Case, async: false

  alias Boxland.Server.Supervisor, as: ServerSup

  setup do
    # test_helper.exs starts the Phoenix children so endpoint-dependent
    # tests work. For supervisor tests, drop to a clean state, then
    # restore on exit so subsequent tests still have an Endpoint.
    initially_running = ServerSup.status() == :running
    ServerSup.stop_children()

    on_exit(fn ->
      if initially_running, do: ServerSup.start_children()
    end)

    pid = Process.whereis(ServerSup)
    assert is_pid(pid)
    {:ok, pid: pid}
  end

  test "starts with zero children after stop_children/0", %{pid: pid} do
    assert Supervisor.count_children(pid).active == 0
  end

  test "status/0 returns :stopped when no children" do
    assert ServerSup.status() == :stopped
  end

  test "start_children/0 then stop_children/0 round-trip" do
    assert ServerSup.status() == :stopped
    assert :ok = ServerSup.start_children()
    assert ServerSup.status() == :running
    assert :ok = ServerSup.stop_children()
    assert ServerSup.status() == :stopped
  end
end
