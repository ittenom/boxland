defmodule Boxland.Scripting.HostTest do
  use ExUnit.Case, async: true
  alias Boxland.Scripting.Host

  describe "evaluate/1" do
    test "runs a trivial Lua expression" do
      assert {:ok, [3]} = Host.evaluate("return 1 + 2")
    end

    test "returns multiple values" do
      assert {:ok, [1, 2, 3]} = Host.evaluate("return 1, 2, 3")
    end

    test "io.* is not available (sandboxed)" do
      assert {:error, _} = Host.evaluate("io.write('hi')")
    end

    test "os.execute is not available (sandboxed)" do
      assert {:error, _} = Host.evaluate("return os.execute('ls')")
    end

    test "returns an error for syntax errors" do
      assert {:error, _} = Host.evaluate("function ( bad")
    end
  end
end
