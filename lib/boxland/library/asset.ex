defmodule Boxland.Library.Asset do
  @moduledoc """
  A user-uploaded game file. Kind-discriminated: sprite, spritesheet, audio.
  Content-addressed via SHA256. The `metadata` jsonb field carries
  kind-specific data (validated by the Asset.Metadata helper module).
  """
  use Ecto.Schema
  import Ecto.Changeset

  @valid_kinds ~w(sprite spritesheet audio)

  schema "assets" do
    field :kind, :string
    field :name, :string
    field :sha256, :binary
    field :content_url, :string
    field :byte_size, :integer
    field :mime_type, :string
    field :metadata, :map, default: %{}

    belongs_to :owner, Boxland.Auth.Designer

    timestamps(type: :utc_datetime)
  end

  def changeset(asset, attrs) do
    asset
    |> cast(attrs, [
      :owner_id,
      :kind,
      :name,
      :sha256,
      :content_url,
      :byte_size,
      :mime_type,
      :metadata
    ])
    |> validate_required([:owner_id, :kind, :name, :sha256, :content_url, :byte_size, :mime_type])
    |> validate_inclusion(:kind, @valid_kinds)
    |> validate_number(:byte_size, greater_than: 0)
    |> unique_constraint(:sha256)
    |> foreign_key_constraint(:owner_id)
  end
end
