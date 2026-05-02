defmodule Boxland.CLI.InstallTest do
  use ExUnit.Case, async: true
  alias Boxland.CLI.Install

  import ExUnit.CaptureIO

  test "main/1 returns 0 on Install.run success" do
    deps = %{
      install_run: fn _ -> {:ok, %{}} end
    }
    assert capture_io(fn ->
      assert Install.main([], deps) == 0
    end) =~ "Install complete"
  end

  test "main/1 returns 1 on failure with error message" do
    deps = %{
      install_run: fn _ -> {:error, %{stage: :docker_check, reason: "no daemon"}} end
    }
    output = capture_io(fn -> assert Install.main([], deps) == 1 end)
    assert output =~ "docker_check"
    assert output =~ "no daemon"
  end
end
