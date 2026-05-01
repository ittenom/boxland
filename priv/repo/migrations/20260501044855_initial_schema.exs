defmodule Boxland.Repo.Migrations.InitialSchema do
  use Ecto.Migration

  def change do
    # === AUTH ===

    create table(:designers) do
      add :email, :string, null: false
      add :password_hash, :string, null: false
      add :display_name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:designers, [:email])

    create table(:designer_sessions) do
      add :designer_id, references(:designers, on_delete: :delete_all), null: false
      add :token_hash, :binary, null: false
      add :ip, :inet
      add :expires_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:designer_sessions, [:token_hash])
    create index(:designer_sessions, [:designer_id])

    create table(:players) do
      add :email, :string
      add :password_hash, :string
      add :display_name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:players, [:email], where: "email IS NOT NULL")

    create table(:player_oauth_links) do
      add :player_id, references(:players, on_delete: :delete_all), null: false
      add :provider, :string, null: false
      add :provider_user_id, :string, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:player_oauth_links, [:provider, :provider_user_id])
    create index(:player_oauth_links, [:player_id])

    create table(:player_sessions) do
      add :player_id, references(:players, on_delete: :delete_all), null: false
      add :refresh_token_hash, :binary, null: false
      add :expires_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:player_sessions, [:refresh_token_hash])
    create index(:player_sessions, [:player_id])

    # === ASSETS ===

    create table(:assets) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :kind, :string, null: false
      add :name, :string, null: false
      add :sha256, :binary, null: false
      add :content_url, :string, null: false
      add :byte_size, :integer, null: false
      add :mime_type, :string, null: false
      add :metadata, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create unique_index(:assets, [:sha256])
    create index(:assets, [:owner_id])
    create index(:assets, [:kind])

    # === MAPS ===

    create table(:maps) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :width, :integer, null: false
      add :height, :integer, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:maps, [:owner_id, :slug])

    create table(:map_layers) do
      add :map_id, references(:maps, on_delete: :delete_all), null: false
      add :name, :string, null: false
      add :z_index, :integer, null: false
      add :tiles, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create unique_index(:map_layers, [:map_id, :name])
    create index(:map_layers, [:map_id, :z_index])

    # === ENTITIES ===

    create table(:entity_types) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :visual_ref, :map, null: false, default: %{}
      add :animation_bindings, :map, null: false, default: %{}
      add :components, {:array, :map}, null: false, default: []
      add :scripts, {:array, :map}, null: false, default: []
      add :default_collision_mask, :string, null: false, default: "land"
      add :default_z_index, :integer, null: false, default: 25
      timestamps(type: :utc_datetime)
    end
    create unique_index(:entity_types, [:owner_id, :slug])

    # === WORLDS ===

    create table(:worlds) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      timestamps(type: :utc_datetime)
    end
    create unique_index(:worlds, [:owner_id, :slug])

    # === LEVELS ===

    create table(:levels) do
      add :owner_id, references(:designers, on_delete: :restrict), null: false
      add :slug, :string, null: false
      add :name, :string, null: false
      add :map_id, references(:maps, on_delete: :restrict), null: false
      add :world_id, references(:worlds, on_delete: :nilify_all)
      add :hud_config, :map, null: false, default: %{}
      add :instancing, :string, null: false, default: "shared"
      timestamps(type: :utc_datetime)
    end
    create unique_index(:levels, [:owner_id, :slug])
    create index(:levels, [:world_id])
    create index(:levels, [:map_id])

    create table(:level_entities) do
      add :level_id, references(:levels, on_delete: :delete_all), null: false
      add :entity_type_id, references(:entity_types, on_delete: :restrict), null: false
      add :pos_x, :integer, null: false
      add :pos_y, :integer, null: false
      add :z_index_override, :integer
      add :instance_overrides, :map, null: false, default: %{}
      add :script_state, :map, null: false, default: %{}
      timestamps(type: :utc_datetime)
    end
    create index(:level_entities, [:level_id])
    create index(:level_entities, [:level_id, :z_index_override])

    # === GAME RUNTIME STATE ===

    create table(:level_state) do
      add :level_id, references(:levels, on_delete: :delete_all), null: false
      add :instance_key, :string, null: false
      add :state, :binary, null: false
      add :flushed_at, :utc_datetime, null: false
      timestamps(type: :utc_datetime, updated_at: false)
    end
    create unique_index(:level_state, [:level_id, :instance_key])
  end
end
