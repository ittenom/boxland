defmodule Boxland.TUI.LoggerHandler do
  @moduledoc """
  Erlang `:logger` handler that forwards every log event to
  `Boxland.TUI.LogBackend`'s ring buffer.

  Register at application start with:

      :logger.add_handler(:boxland_tui, Boxland.TUI.LoggerHandler, %{})
  """

  alias Boxland.TUI.LogBackend

  def adding_handler(config), do: {:ok, config}
  def removing_handler(_config), do: :ok
  def changing_config(_set_or_update, _old, new), do: {:ok, new}

  def log(%{level: level, msg: msg, meta: meta}, _config) do
    if Process.whereis(LogBackend) do
      formatted = format_msg(msg)
      time = system_time_to_datetime(meta[:time])
      LogBackend.log(level, formatted, %{time: time})
    end

    :ok
  end

  defp format_msg({:string, chardata}), do: IO.chardata_to_string(chardata)
  defp format_msg({:report, report}) when is_map(report), do: inspect(report)
  defp format_msg({:report, report}) when is_list(report), do: inspect(report)

  defp format_msg({format, args}) when is_list(args) do
    format |> :io_lib.format(args) |> IO.chardata_to_string()
  rescue
    _ -> inspect({format, args})
  end

  defp format_msg(other), do: inspect(other)

  defp system_time_to_datetime(nil), do: DateTime.utc_now()

  defp system_time_to_datetime(microseconds) when is_integer(microseconds) do
    DateTime.from_unix!(microseconds, :microsecond)
  end
end
