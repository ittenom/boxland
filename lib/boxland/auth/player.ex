defmodule Boxland.Auth.Player do
  @moduledoc """
  A game runtime user. Authenticated via JWT (access + refresh tokens).
  Email + password OR OAuth (Google, Apple, Discord).
  """
  use Ecto.Schema
  import Ecto.Changeset

  schema "players" do
    field :email, :string
    field :password_hash, :string
    field :display_name, :string

    has_many :oauth_links, Boxland.Auth.PlayerOAuthLink
    has_many :sessions, Boxland.Auth.PlayerSession

    timestamps(type: :utc_datetime)
  end

  def changeset(player, attrs) do
    player
    |> cast(attrs, [:email, :password_hash, :display_name])
    |> validate_required([:display_name])
    |> validate_format(:email, ~r/@/, message: "must contain @ if provided", allow_nil: true)
    |> update_change(:email, fn
      nil -> nil
      v -> String.downcase(v)
    end)
    |> unique_constraint(:email)
  end
end
