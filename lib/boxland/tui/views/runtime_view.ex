defmodule Boxland.TUI.Views.RuntimeView do
  @moduledoc """
  Renders the runtime view — what the user sees when "Run Server" is ON.
  Compact logo + status strip + log pane + key hints footer.
  """

  alias Boxland.TUI.{ServerRuntime, Theme}

  @doc """
  State keys:
    - :url — String.t
    - :elapsed_ms — non_neg_integer
    - :log_lines — list of strings (most recent last)
    - :status — :running | :stopping | :stopped | {:error, reason}
  """
  def render(state) do
    %{
      type: :container,
      children: [
        compact_logo(),
        status_strip(state),
        log_pane(state.log_lines),
        key_hints()
      ]
    }
  end

  defp compact_logo do
    %{
      type: :logo_compact,
      content: Theme.logo_compact(),
      gradient: {Theme.colors().accent_warm, Theme.colors().accent_warm_end}
    }
  end

  defp status_strip(state) do
    label =
      case state.status do
        :running -> "● Server running #{ServerRuntime.format_elapsed(state.elapsed_ms)}"
        :stopping -> "⏵ Stopping…"
        :stopped -> "○ Stopped"
        {:error, reason} -> "✗ Failed: #{reason}"
      end

    %{
      type: :status_strip,
      left: label,
      right: state.url <> " ↗",
      color: status_color(state.status)
    }
  end

  defp status_color(:running), do: :success
  defp status_color(:stopping), do: :warning
  defp status_color(:stopped), do: :text_muted
  defp status_color({:error, _}), do: :error

  defp log_pane(lines) do
    %{type: :log_pane, lines: lines}
  end

  defp key_hints do
    %{
      type: :key_hints,
      content: "[Esc] Stop server   [Q] Quit   [↑↓ PgUp/PgDn] Scroll   [Home/End] Jump"
    }
  end

  @doc "Test helper: flattens to strings."
  def flatten_for_test(tree) when is_map(tree) do
    case tree do
      %{type: :container, children: kids} -> Enum.flat_map(kids, &flatten_for_test/1)
      %{type: :logo_compact, content: c} -> String.split(c, "\n")
      %{type: :status_strip, left: l, right: r} -> ["#{l}    #{r}"]
      %{type: :log_pane, lines: lines} -> lines
      %{type: :key_hints, content: c} -> [c]
      _ -> []
    end
  end
end
