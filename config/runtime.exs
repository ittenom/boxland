import Config

if System.get_env("PHX_SERVER") do
  config :boxland, BoxlandWeb.Endpoint, server: true
end

if designer_email_domain = System.get_env("DESIGNER_EMAIL_DOMAIN") do
  config :boxland, :designer_email_domain, designer_email_domain
end

# Object storage (Cloudflare R2 via S3 API). Applied in any env when env vars are set.
if cdn_base_url = System.get_env("CDN_BASE_URL") do
  config :boxland, :cdn_base_url, cdn_base_url
end

if s3_host = System.get_env("S3_HOST") do
  config :ex_aws, :s3,
    host: s3_host,
    bucket: System.get_env("S3_BUCKET"),
    scheme: System.get_env("S3_SCHEME") || "https://",
    region: System.get_env("S3_REGION") || "auto"
end

if s3_access_key_id = System.get_env("S3_ACCESS_KEY_ID") do
  config :ex_aws, access_key_id: s3_access_key_id
end

if s3_secret_access_key = System.get_env("S3_SECRET_ACCESS_KEY") do
  config :ex_aws, secret_access_key: s3_secret_access_key
end

if config_env() == :prod do
  maybe_ipv6 =
    if System.get_env("ECTO_IPV6") in ~w(true 1) do
      [:inet6]
    else
      []
    end

  config :boxland, Boxland.Repo,
    url: System.fetch_env!("DATABASE_URL"),
    pool_size: String.to_integer(System.get_env("POOL_SIZE") || "10"),
    socket_options: maybe_ipv6

  host =
    System.get_env("PHX_HOST") ||
      System.get_env("RAILWAY_PUBLIC_DOMAIN") ||
      "localhost"

  port = String.to_integer(System.get_env("PORT") || "4000")

  config :boxland, BoxlandWeb.Endpoint,
    url: [host: host, port: 443, scheme: "https"],
    http: [ip: {0, 0, 0, 0}, port: port],
    secret_key_base: System.fetch_env!("SECRET_KEY_BASE")

  if redis_url = System.get_env("REDIS_URL") do
    config :boxland, :redis_url, redis_url
  end
end
