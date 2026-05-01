defmodule Boxland.Scripting.Host do
  @moduledoc """
  Sandboxed Luerl runtime for entity scripts.

  v1 sandbox: blocks `io.*`, `os.execute`, `loadstring`, `load`, `dofile`, `require`.
  Per-call instruction/memory/wall-clock limits will be added when the
  Game Runtime spec wires entity tick-loop calls through here.
  """

  @doc """
  Evaluate a Lua source string in a fresh sandboxed Luerl state.
  Returns `{:ok, results}` where results is the list of return values,
  or `{:error, reason}` on parse/runtime/sandbox error.
  """
  @spec evaluate(String.t()) :: {:ok, list()} | {:error, term()}
  def evaluate(source) when is_binary(source) do
    state = sandboxed_state()

    try do
      case :luerl.do(source, state) do
        {:ok, results, _new_state} -> {:ok, results}
        {:lua_error, reason, _state} -> {:error, reason}
        {:error, errors, _} -> {:error, errors}
      end
    rescue
      e -> {:error, Exception.message(e)}
    catch
      kind, reason -> {:error, {kind, reason}}
    end
  end

  @doc "Build a fresh Luerl state with the sandbox locks applied."
  def sandboxed_state do
    :luerl_sandbox.init()
  end
end
