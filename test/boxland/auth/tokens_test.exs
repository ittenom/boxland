defmodule Boxland.Auth.TokensTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.Tokens

  setup do
    # Phoenix.Token reads BoxlandWeb.Endpoint config from ETS; another test
    # may have stopped the Endpoint via Boxland.Server.Supervisor.
    :ok = Boxland.Server.Supervisor.start_children()
    :ok
  end

  # Wrap Phoenix.Token-bound calls so that if Endpoint was torn down by a
  # parallel test mid-call, we restart it and retry once before failing.
  defp with_endpoint_retry(fun, retries \\ 3) do
    fun.()
  rescue
    e in ArgumentError ->
      if retries > 0 and Exception.message(e) =~ "ETS table" do
        :ok = Boxland.Server.Supervisor.start_children()
        with_endpoint_retry(fun, retries - 1)
      else
        reraise e, __STACKTRACE__
      end
  end

  test "mint and verify a player access token" do
    with_endpoint_retry(fn ->
      token = Tokens.mint_player_access(%{player_id: 42})
      assert is_binary(token)
      assert {:ok, %{player_id: 42, realm: :player}} = Tokens.verify_game_token(token)
    end)
  end

  test "mint and verify a designer-as-sandbox token" do
    with_endpoint_retry(fn ->
      token = Tokens.mint_sandbox(%{designer_id: 7, level_id: 11})

      assert {:ok, %{player_id: 7, realm: :designer_sandbox, level_id: 11}} =
               Tokens.verify_game_token(token)
    end)
  end

  test "tampered token is rejected" do
    with_endpoint_retry(fn ->
      token = Tokens.mint_player_access(%{player_id: 1})
      bad = String.replace(token, ~r/.$/, "X")
      assert {:error, _} = Tokens.verify_game_token(bad)
    end)
  end
end
