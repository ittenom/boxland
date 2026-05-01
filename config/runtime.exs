import Config

if System.get_env("PHX_SERVER") do
  config :boxland, BoxlandWeb.Endpoint, server: true
end

if config_env() == :prod do
  database_url =
    System.get_env("DATABASE_URL") ||
      raise """
      environment variable DATABASE_URL is missing.
      For example: ecto://USER:PASS@HOST/DATABASE
      """

  maybe_ipv6 = if System.get_env("ECTO_IPV6") in ~w(true 1), do: [:inet6], else: []

  config :boxland, Boxland.Repo,
    url: database_url,
    pool_size: String.to_integer(System.get_env("POOL_SIZE") || "10"),
    socket_options: maybe_ipv6

  secret_key_base =
    System.get_env("SECRET_KEY_BASE") ||
      raise "environment variable SECRET_KEY_BASE is missing."

  host = System.get_env("PHX_HOST") || "example.com"
  port = String.to_integer(System.get_env("PORT") || "4000")

  config :boxland, BoxlandWeb.Endpoint,
    url: [host: host, port: 443, scheme: "https"],
    http: [ip: {0, 0, 0, 0, 0, 0, 0, 0}, port: port],
    secret_key_base: secret_key_base

  # Redis
  config :boxland, :redis_url, System.fetch_env!("REDIS_URL")

  # Object storage (S3-compatible)
  config :ex_aws,
    access_key_id: System.fetch_env!("S3_ACCESS_KEY_ID"),
    secret_access_key: System.fetch_env!("S3_SECRET_ACCESS_KEY")

  config :ex_aws, :s3,
    host: System.fetch_env!("S3_HOST"),
    bucket: System.fetch_env!("S3_BUCKET"),
    scheme: "https://"

  config :boxland, :cdn_base_url, System.fetch_env!("CDN_BASE_URL")

  # OAuth
  config :boxland, :oauth,
    google: [
      client_id: System.get_env("GOOGLE_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("GOOGLE_OAUTH_CLIENT_SECRET")
    ],
    apple: [
      client_id: System.get_env("APPLE_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("APPLE_OAUTH_CLIENT_SECRET")
    ],
    discord: [
      client_id: System.get_env("DISCORD_OAUTH_CLIENT_ID"),
      client_secret: System.get_env("DISCORD_OAUTH_CLIENT_SECRET")
    ]

  # Mailer
  config :boxland, Boxland.Mailer,
    adapter: Swoosh.Adapters.Postmark,
    api_key: System.fetch_env!("POSTMARK_API_KEY")

  config :swoosh, :api_client, Swoosh.ApiClient.Finch
end
