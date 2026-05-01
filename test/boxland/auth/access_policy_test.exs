defmodule Boxland.Auth.AccessPolicyTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.AccessPolicy

  test "player can join the shared instance for a level" do
    assert :ok = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:shared")
  end

  test "player can join their own user instance" do
    assert :ok = AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:user:99")
  end

  test "player cannot join another player's user instance" do
    assert {:error, _} =
             AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:user:42")
  end

  test "player cannot join a sandbox instance" do
    assert {:error, _} =
             AccessPolicy.allow_join?(%{realm: :player, player_id: 99}, "level:42:sandbox:7")
  end

  test "designer-sandbox can only join own sandbox" do
    assigns = %{realm: :designer_sandbox, player_id: 7, level_id: 42}
    assert :ok = AccessPolicy.allow_join?(assigns, "level:42:sandbox:7")
    assert {:error, _} = AccessPolicy.allow_join?(assigns, "level:42:sandbox:8")
    assert {:error, _} = AccessPolicy.allow_join?(assigns, "level:42:shared")
  end
end
