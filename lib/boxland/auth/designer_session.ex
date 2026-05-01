defmodule Boxland.Auth.DesignerSession do
  @moduledoc """
  A single designer login session. Cookie carries an opaque random token
  whose sha256 is stored as `token_hash`. Sliding-window 30-day TTL.

  Note: the underlying `inet` Postgres column is read/written as a string
  in v1 to avoid pulling in ecto_network. If we need typed IP operations
  later we can swap the field type without a migration.
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "designer_sessions" do
    field :token_hash, :binary
    field :ip, :string
    field :expires_at, :utc_datetime

    belongs_to :designer, Boxland.Auth.Designer

    timestamps(type: :utc_datetime, updated_at: false)
  end

  def changeset(session, attrs) do
    session
    |> cast(attrs, [:designer_id, :token_hash, :ip, :expires_at])
    |> validate_required([:designer_id, :token_hash, :expires_at])
    |> unique_constraint(:token_hash)
    |> foreign_key_constraint(:designer_id)
  end
end
