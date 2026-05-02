defmodule Boxland.Server.SupervisorTest do
  use ExUnit.Case, async: false

  @moduletag :supervisor_lifecycle

  alias Boxland.Server.Supervisor, as: ServerSup

  setup_all do
    on_exit(fn -> :ok = ServerSup.start_children() end)
    :ok
  end

  setup do
    ServerSup.stop_children()
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
