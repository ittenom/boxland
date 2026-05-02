defmodule Boxland.CLI.RunTest do
  use ExUnit.Case, async: false
  alias Boxland.CLI.Run

  setup do
    Boxland.Server.Supervisor.stop_children()
    on_exit(fn -> Boxland.Server.Supervisor.stop_children() end)
    :ok
  end

  test "start/0 brings up the server children" do
    Run.start()
    assert Boxland.Server.Supervisor.status() == :running
  end
end
