defmodule Boxland.Auth.PasswordTest do
  use ExUnit.Case, async: true
  alias Boxland.Auth.Password

  test "hash returns a string" do
    assert is_binary(Password.hash("secret"))
  end

  test "verify returns true for the matching plaintext" do
    hash = Password.hash("secret")
    assert Password.verify(hash, "secret")
  end

  test "verify returns false for a wrong plaintext" do
    hash = Password.hash("secret")
    refute Password.verify(hash, "wrong")
  end

  test "verify against a nil hash returns false" do
    refute Password.verify(nil, "anything")
  end
end
