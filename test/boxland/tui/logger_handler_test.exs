defmodule Boxland.TUI.LoggerHandlerTest do
  use ExUnit.Case, async: false

  alias Boxland.TUI.{LogBackend, LoggerHandler}

  setup do
    # Ensure LogBackend is up — other test files may have stopped it.
    case Process.whereis(LogBackend) do
      nil -> {:ok, _} = LogBackend.start_link([])
      _ -> :ok
    end

    LogBackend.flush()
    LogBackend.subscribe(self())

    on_exit(fn ->
      if Process.whereis(LogBackend) do
        try do
          LogBackend.unsubscribe(self())
        catch
          :exit, _ -> :ok
        end
      end
    end)

    :ok
  end

  describe "log/2" do
    test "string-style msg lands in the ring buffer with the formatted payload" do
      event = %{
        level: :info,
        msg: {:string, "hello world"},
        meta: %{time: System.system_time(:microsecond)}
      }

      LoggerHandler.log(event, %{})

      assert_receive {:log_entry, line}, 200
      assert line =~ "[info]"
      assert line =~ "hello world"
    end

    test "format-style msg with args is rendered" do
      event = %{
        level: :warning,
        msg: {~c"hello ~s", [~c"there"]},
        meta: %{time: System.system_time(:microsecond)}
      }

      LoggerHandler.log(event, %{})

      assert_receive {:log_entry, line}, 200
      assert line =~ "[warning]"
      assert line =~ "hello there"
    end

    test "report-style map msg is inspected" do
      event = %{
        level: :error,
        msg: {:report, %{reason: :crash, attempts: 3}},
        meta: %{time: System.system_time(:microsecond)}
      }

      LoggerHandler.log(event, %{})

      assert_receive {:log_entry, line}, 200
      assert line =~ "[error]"
      assert line =~ "reason"
      assert line =~ ":crash"
    end

    test "events without meta.time still flow" do
      event = %{level: :info, msg: {:string, "no time"}, meta: %{}}

      LoggerHandler.log(event, %{})

      assert_receive {:log_entry, line}, 200
      assert line =~ "no time"
    end
  end
end
