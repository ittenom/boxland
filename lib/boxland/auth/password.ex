defmodule Boxland.Auth.Password do
  @moduledoc "Argon2 wrapper. Single hashing function for both auth realms."

  @doc "Hash a plaintext password. Returns the encoded Argon2 string."
  def hash(plaintext) when is_binary(plaintext) do
    Argon2.hash_pwd_salt(plaintext)
  end

  @doc """
  Verify a plaintext against a stored hash. Returns true on match.
  Returns false (without raising) if hash is nil — useful when checking
  OAuth-only players who have no password.
  """
  def verify(nil, _plaintext), do: Argon2.no_user_verify() && false

  def verify(hash, plaintext) when is_binary(hash) and is_binary(plaintext) do
    Argon2.verify_pass(plaintext, hash)
  end
end
