defmodule Boxland.TUI.App do
  @moduledoc """
  Boxland's terminal launcher app, built on `TermUI.Elm`.

  Three screens:
    * `:menu` — main menu (Install, Run Server, Quit)
    * `:installing` — running install workflow (live log feed visible)
    * `:running` — server is up; status + log feed visible

  Subscribes to `Boxland.TUI.LogBackend` on init for the live log pane.
  """

  use TermUI.Elm

  alias Boxland.TUI.{Install, LogBackend, Menu, ServerRuntime, Theme}
  alias TermUI.Event
  alias TermUI.Renderer.Style

  @width 76
  @log_pane_height 8

  # ---------- Init ----------

  @impl true
  def init(_opts) do
    marker = Install.read_marker()
    installed_at = if marker, do: marker.installed_at, else: nil
    current_version = to_string(Application.spec(:boxland, :vsn))
    upgrade = Menu.upgrade_pending?(marker && marker.version, current_version)

    if Process.whereis(LogBackend), do: LogBackend.subscribe(self())

    %{
      screen: :menu,
      selected_index: 0,
      installed_at: installed_at,
      upgrade_pending: upgrade,
      version: current_version,
      data_dir: Path.expand("~/.boxland"),
      log_lines: [],
      install_progress: nil,
      install_error: nil,
      runtime_started_at: nil,
      install_runner: &Install.run/0
    }
  end

  # ---------- Event mapping ----------

  @impl true
  def event_to_msg(%Event.Key{key: key}, _state) when key in ["q", "Q"], do: {:msg, :quit}
  def event_to_msg(%Event.Key{key: :down}, _state), do: {:msg, :select_next}
  def event_to_msg(%Event.Key{key: :up}, _state), do: {:msg, :select_prev}
  def event_to_msg(%Event.Key{key: "j"}, _state), do: {:msg, :select_next}
  def event_to_msg(%Event.Key{key: "k"}, _state), do: {:msg, :select_prev}
  def event_to_msg(%Event.Key{key: :enter}, _state), do: {:msg, :activate}
  def event_to_msg(_, _state), do: :ignore

  # ---------- Update ----------

  @impl true
  def update(:quit, state) do
    if ServerRuntime.status() == :running, do: ServerRuntime.stop()
    {state, [:quit]}
  end

  def update(:select_next, %{screen: :menu} = state) do
    items = current_items(state)
    new_idx = Menu.next_selectable(items, state.selected_index, :down) || state.selected_index
    {%{state | selected_index: new_idx}, []}
  end

  def update(:select_prev, %{screen: :menu} = state) do
    items = current_items(state)
    new_idx = Menu.next_selectable(items, state.selected_index, :up) || state.selected_index
    {%{state | selected_index: new_idx}, []}
  end

  def update(:activate, %{screen: :menu} = state) do
    items = current_items(state)
    item = Enum.at(items, state.selected_index)

    cond do
      item == nil or item.disabled? ->
        {state, []}

      item.id in [:install, :recheck_install] ->
        target = self()
        runner = state.install_runner

        spawn(fn ->
          result = runner.()
          send(target, {:install_finished, result})
        end)

        {%{
           state
           | screen: :installing,
             install_progress: "Running install workflow…",
             install_error: nil
         }, []}

      item.id == :run_server ->
        case ServerRuntime.start() do
          :ok ->
            {%{
               state
               | screen: :running,
                 runtime_started_at: ServerRuntime.started_at(),
                 selected_index: 0
             }, []}

          {:error, reason} ->
            {%{state | install_error: inspect(reason)}, []}
        end

      item.id == :stop_server ->
        ServerRuntime.stop()
        {%{state | screen: :menu, runtime_started_at: nil, selected_index: 0}, []}

      item.id == :quit ->
        update(:quit, state)
    end
  end

  def update(:activate, %{screen: :running} = state) do
    ServerRuntime.stop()
    {%{state | screen: :menu, runtime_started_at: nil, selected_index: 0}, []}
  end

  def update({:install_finished, {:ok, _report}}, state) do
    marker = Install.read_marker()
    installed_at = if marker, do: marker.installed_at, else: nil

    {%{
       state
       | screen: :menu,
         install_progress: nil,
         install_error: nil,
         installed_at: installed_at,
         upgrade_pending: false,
         selected_index: 0
     }, []}
  end

  def update({:install_finished, {:error, err}}, state) do
    msg =
      case err do
        %{stage: stage, reason: reason} -> "[#{stage}] #{reason}"
        other -> inspect(other)
      end

    {%{
       state
       | screen: :menu,
         install_progress: nil,
         install_error: msg,
         selected_index: 0
     }, []}
  end

  def update({:log_entry, line}, state) do
    {%{state | log_lines: trim_log(state.log_lines ++ [line])}, []}
  end

  def update({:log_buffer, entries}, state) do
    {%{state | log_lines: trim_log(entries)}, []}
  end

  def update(_msg, state), do: {state, []}

  # ---------- View ----------

  @impl true
  def view(state) do
    stack(:vertical, [
      logo_block(),
      blank(),
      status_row(state),
      blank(),
      main_panel(state),
      blank(),
      log_panel(state.log_lines),
      blank(),
      help_bar(state)
    ])
  end

  # ---------- View helpers ----------

  defp logo_block do
    lines = String.split(Theme.logo_full(), "\n")
    total = length(lines)

    rows =
      lines
      |> Enum.with_index()
      |> Enum.map(fn {line, idx} ->
        rgb = Theme.gradient_for_column(idx, total)
        text(line, Style.new(fg: rgb, attrs: [:bold]))
      end)

    stack(:vertical, rows)
  end

  defp status_row(state) do
    stack(:horizontal, [
      status_panel(state),
      text("  ", nil),
      server_panel(state),
      text("  ", nil),
      version_panel(state)
    ])
  end

  defp status_panel(state) do
    {indicator, color} =
      cond do
        state.installed_at != nil and state.upgrade_pending -> {"◐ Upgrade ready", :warning}
        state.installed_at != nil -> {"● Installed", :success}
        true -> {"○ Not installed", :text_muted}
      end

    body =
      case state.installed_at do
        nil -> ["Run Install to set up.", ""]
        dt -> [Calendar.strftime(dt, "%Y-%m-%d %H:%M"), state.data_dir]
      end

    bordered_panel("Install", 24, [
      text(indicator, color_style(color, [:bold]))
      | Enum.map(body, &text(&1, color_style(:text_muted)))
    ])
  end

  defp server_panel(state) do
    {indicator, color, detail} =
      cond do
        state.screen == :running ->
          elapsed = ServerRuntime.format_elapsed(ServerRuntime.elapsed(state.runtime_started_at))
          {"● Running", :success, "uptime #{elapsed}"}

        true ->
          {"○ Stopped", :text_muted, "http://localhost:4000"}
      end

    bordered_panel("Server", 24, [
      text(indicator, color_style(color, [:bold])),
      text(detail, color_style(:text_muted)),
      text("", nil)
    ])
  end

  defp version_panel(state) do
    bordered_panel("Version", 22, [
      text("Boxland #{state.version}", color_style(:text, [:bold])),
      text("Elixir / Phoenix", color_style(:text_muted)),
      text("", nil)
    ])
  end

  defp main_panel(%{screen: :installing} = state) do
    bordered_panel("Install in progress", @width, [
      text("⏳ #{state.install_progress || "Working…"}", color_style(:warning, [:bold])),
      text(
        "Watching log feed below — this can take 30-60s on first run.",
        color_style(:text_muted)
      )
    ])
  end

  defp main_panel(state) do
    items = current_items(state)
    selected = state.selected_index
    err_rows = error_rows(state)

    item_rows =
      items
      |> Enum.with_index()
      |> Enum.map(fn {item, idx} -> menu_row(item, idx == selected) end)

    bordered_panel("Menu", @width, err_rows ++ item_rows)
  end

  defp menu_row(item, selected?) do
    label = String.pad_trailing("#{item.glyph}  #{item.label}", 22)
    desc = item.description

    line = " #{label}#{desc}"
    line = String.pad_trailing(line, @width - 4)

    style =
      cond do
        selected? -> Style.new(fg: rgb(:text), bg: rgb(:accent_warm_end), attrs: [:bold])
        item.disabled? -> color_style(:text_subtle)
        item.style == :featured -> color_style(:accent_cool, [:bold])
        item.style == :demoted -> color_style(:text_muted)
        true -> color_style(:text)
      end

    text(line, style)
  end

  defp error_rows(%{install_error: nil}), do: []

  defp error_rows(%{install_error: msg}) do
    [
      text("✗ Install failed: #{msg}", color_style(:error, [:bold])),
      text("", nil)
    ]
  end

  defp log_panel(lines) do
    visible =
      lines
      |> Enum.take(-@log_pane_height)
      |> pad_to(@log_pane_height)
      |> Enum.map(fn l -> text(truncate(l, @width - 4), color_style(:text_muted)) end)

    bordered_panel("Logs", @width, visible)
  end

  defp help_bar(state) do
    keys =
      case state.screen do
        :menu -> "[↑↓ j/k] Navigate   [Enter] Select   [Q] Quit"
        :installing -> "[Q] Cancel + Quit"
        :running -> "[Enter] Stop server   [Q] Quit"
      end

    text(" #{keys}", color_style(:text_muted))
  end

  # ---------- Bordered panel helper ----------

  defp bordered_panel(title, width, body_nodes) do
    border_style = color_style(:border)

    title_str = " " <> title <> " "
    title_len = String.length(title_str)
    fill = max(width - title_len - 4, 0)

    top = "┌─" <> title_str <> String.duplicate("─", fill) <> "─┐"
    bottom = "└" <> String.duplicate("─", width - 2) <> "┘"

    body_lines =
      Enum.map(body_nodes, fn node ->
        stack(:horizontal, [
          text("│ ", border_style),
          node,
          text(" │", border_style)
        ])
      end)

    stack(:vertical, [text(top, border_style)] ++ body_lines ++ [text(bottom, border_style)])
  end

  # ---------- Misc helpers ----------

  defp trim_log(lines), do: Enum.take(lines, -200)

  defp current_items(state) do
    Menu.items(
      installed_at: state.installed_at,
      server_status: server_status(state),
      upgrade_pending: state.upgrade_pending
    )
  end

  defp server_status(%{screen: :running}), do: :running
  defp server_status(_), do: :stopped

  defp color_style(token, attrs \\ []) do
    Style.new(fg: rgb(token), attrs: attrs)
  end

  defp rgb(token), do: Map.fetch!(Theme.colors(), token)

  defp blank, do: text("", nil)

  defp pad_to(lines, n) when length(lines) >= n, do: lines

  defp pad_to(lines, n) do
    lines ++ List.duplicate("", n - length(lines))
  end

  defp truncate(line, max) when byte_size(line) <= max, do: line
  defp truncate(line, max), do: String.slice(line, 0, max - 1) <> "…"
end
