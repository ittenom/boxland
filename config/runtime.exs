import Config

if System.get_env("PHX_SERVER") do
  config :boxland, BoxlandWeb.Endpoint, server: true
end

if config_env() == :prod do
  user_config_path = Path.expand("~/.boxland/config.exs")

  if File.exists?(user_config_path) do
    # Boxland TUI install has run; load user-generated config.
    Code.eval_file(user_config_path)
  else
    # Pre-install state: skip prod config requirements.
    # The TUI handles missing-config by routing the user to Install.
    # Phoenix children will fail to start until Install completes,
    # which the TUI handles gracefully.
    :ok
  end
end
