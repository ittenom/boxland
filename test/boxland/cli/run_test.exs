defmodule Boxland.CLI.RunTest do
  use ExUnit.Case, async: false

  @moduletag :supervisor_lifecycle

  alias Boxland.CLI.Run

  setup_all do
    on_exit(fn -> :ok = Boxland.Server.Supervisor.start_children() end)
    :ok
  end

  setup do
    Boxland.Server.Supervisor.stop_children()
    :ok
  end

  test "start/0 brings up the server children" do
    Run.start()
    assert Boxland.Server.Supervisor.status() == :running
  end
end
