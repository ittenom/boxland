defmodule Boxland.Auth.Designers do
  @moduledoc """
  Designer registration, authentication, and session management.
  Sessions are cookie-backed: a random token is generated, sha256 stored
  in the DB, plain token returned to be set as the cookie value.
  """

  import Ecto.Query
  alias Boxland.Repo
  alias Boxland.Auth.{Designer, DesignerSession, Password}

  # 30 days
  @session_ttl_seconds 60 * 60 * 24 * 30
  @session_token_bytes 32

  @doc "Register a new designer. Hashes the plaintext password with Argon2."
  def register_designer(attrs) do
    case validate_password(attrs) do
      :ok ->
        attrs =
          Map.put(attrs, :password_hash, Password.hash(attrs[:password] || attrs["password"]))

        %Designer{}
        |> Designer.changeset(attrs)
        |> Repo.insert()

      {:error, changeset} ->
        {:error, changeset}
    end
  end

  @doc "Authenticate by email + plaintext password. Returns {:ok, designer} or :error."
  def authenticate(email, password) when is_binary(email) and is_binary(password) do
    designer = Repo.get_by(Designer, email: String.downcase(email))

    cond do
      designer && Password.verify(designer.password_hash, password) ->
        {:ok, designer}

      designer ->
        :error

      true ->
        Password.verify(nil, password)
        :error
    end
  end

  @doc """
  Create a new session for a designer. Returns `{:ok, plain_token}`.
  The plain token must be set as the client's cookie value.
  """
  def create_session(designer_id, ip) do
    plain_token =
      :crypto.strong_rand_bytes(@session_token_bytes) |> Base.url_encode64(padding: false)

    token_hash = :crypto.hash(:sha256, plain_token)

    expires_at =
      DateTime.utc_now() |> DateTime.add(@session_ttl_seconds) |> DateTime.truncate(:second)

    case %DesignerSession{}
         |> DesignerSession.changeset(%{
           designer_id: designer_id,
           token_hash: token_hash,
           ip: ip,
           expires_at: expires_at
         })
         |> Repo.insert() do
      {:ok, _session} -> {:ok, plain_token}
      {:error, changeset} -> {:error, changeset}
    end
  end

  @doc "Fetch the designer for a session token. Returns {:ok, designer} | :not_found | :expired."
  def fetch_session(plain_token) when is_binary(plain_token) do
    token_hash = :crypto.hash(:sha256, plain_token)
    now = DateTime.utc_now()

    query =
      from s in DesignerSession,
        join: d in assoc(s, :designer),
        where: s.token_hash == ^token_hash,
        select: {s, d}

    case Repo.one(query) do
      nil ->
        :not_found

      {%DesignerSession{expires_at: expires}, designer} ->
        if DateTime.compare(expires, now) == :lt do
          :expired
        else
          {:ok, designer}
        end
    end
  end

  @doc "Revoke (delete) a session by its plain token."
  def revoke_session(plain_token) do
    token_hash = :crypto.hash(:sha256, plain_token)
    Repo.delete_all(from s in DesignerSession, where: s.token_hash == ^token_hash)
    :ok
  end

  defp validate_password(%{password: pw}) when is_binary(pw) and byte_size(pw) >= 10, do: :ok
  defp validate_password(%{"password" => pw}) when is_binary(pw) and byte_size(pw) >= 10, do: :ok

  defp validate_password(_attrs) do
    changeset =
      %Designer{}
      |> Ecto.Changeset.change()
      |> Ecto.Changeset.add_error(:password, "must be at least 10 characters")

    {:error, changeset}
  end
end
