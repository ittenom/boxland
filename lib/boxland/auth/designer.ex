defmodule Boxland.Auth.Designer do
  @moduledoc """
  An IDE user. Owns assets, maps, levels, entities, worlds.
  Authenticated via cookie-backed session (DesignerSession).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "designers" do
    field :email, :string
    field :password_hash, :string
    field :display_name, :string

    has_many :sessions, Boxland.Auth.DesignerSession

    timestamps(type: :utc_datetime)
  end

  def changeset(designer, attrs) do
    designer
    |> cast(attrs, [:email, :password_hash, :display_name])
    |> validate_required([:email, :password_hash, :display_name])
    |> validate_format(:email, ~r/@/)
    |> update_change(:email, &String.downcase/1)
    |> unique_constraint(:email)
  end
end
