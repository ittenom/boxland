defmodule Boxland.Auth.TokensTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.Tokens

  test "mint and verify a player access token" do
    token = Tokens.mint_player_access(%{player_id: 42})
    assert is_binary(token)
    assert {:ok, %{player_id: 42, realm: :player}} = Tokens.verify_game_token(token)
  end

  test "mint and verify a designer-as-sandbox token" do
    token = Tokens.mint_sandbox(%{designer_id: 7, level_id: 11})
    assert {:ok, %{player_id: 7, realm: :designer_sandbox, level_id: 11}} = Tokens.verify_game_token(token)
  end

  test "tampered token is rejected" do
    token = Tokens.mint_player_access(%{player_id: 1})
    bad = String.replace(token, ~r/.$/, "X")
    assert {:error, _} = Tokens.verify_game_token(bad)
  end
end
