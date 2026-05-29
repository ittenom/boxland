defmodule BoxlandWeb.Router do
  use BoxlandWeb, :router
  import BoxlandWeb.DesignerAuth

  pipeline :browser do
    plug :accepts, ["html"]
    plug :fetch_session
    plug :fetch_current_designer
    plug :fetch_live_flash
    plug :put_root_layout, html: {BoxlandWeb.Layouts, :root}
    plug :protect_from_forgery
    plug :put_secure_browser_headers
  end

  pipeline :designer do
    plug :require_designer
  end

  pipeline :api do
    plug :accepts, ["json"]
  end

  scope "/", BoxlandWeb do
    pipe_through :browser

    get "/", PageController, :home
    live "/register", DesignerRegisterLive
    post "/register", DesignerRegistrationController, :create
    live "/login", DesignerLoginLive
    post "/login", DesignerSessionController, :create
    delete "/logout", DesignerSessionController, :delete
  end

  scope "/app", BoxlandWeb do
    pipe_through [:browser, :designer]

    live_session :designer_required,
      on_mount: [{BoxlandWeb.DesignerAuth, :require_designer}] do
      live "/", DashboardLive
      live "/assets", AssetLive
      live "/maps", MapIndexLive
      live "/maps/:id", MapmakerLive
      live "/levels", LevelIndexLive
      live "/levels/:id", LevelEditorLive
    end
  end

  scope "/", BoxlandWeb do
    get "/healthz", HealthController, :healthz
    get "/readyz", HealthController, :readyz
  end

  scope "/", BoxlandWeb do
    pipe_through :browser

    live "/play/:id", PublishedLevelLive
  end

  # Other scopes may use custom stacks.
  # scope "/api", BoxlandWeb do
  #   pipe_through :api
  # end

  # Enable LiveDashboard and Swoosh mailbox preview in development
  if Application.compile_env(:boxland, :dev_routes) do
    # If you want to use the LiveDashboard in production, you should put
    # it behind authentication and allow only admins to access it.
    # If your application does not have an admins-only section yet,
    # you can use Plug.BasicAuth to set up some basic authentication
    # as long as you are also using SSL (which you should anyway).
    import Phoenix.LiveDashboard.Router

    scope "/dev" do
      pipe_through :browser

      live_dashboard "/dashboard", metrics: BoxlandWeb.Telemetry
      forward "/mailbox", Plug.Swoosh.MailboxPreview
    end
  end
end
