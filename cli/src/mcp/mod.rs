mod auth;
#[cfg(test)]
mod tests;

use std::sync::Arc;

use anyhow::Result;
use axum::{
    Json, Router,
    extract::{Request, State},
    http::{StatusCode, header, request::Parts},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::get,
};
use rmcp::{
    ErrorData, ServerHandler,
    handler::server::{router::tool::ToolRouter, tool::Extension},
    model::{CallToolResult, ServerCapabilities, ServerConfig},
    tool, tool_handler, tool_router,
    transport::streamable_http_server::{
        StreamableHttpServerConfig, StreamableHttpService, session::local::LocalSessionManager,
    },
};
use serde_json::json;

use auth::{Identity, Settings};

#[derive(Clone)]
struct Tools {
    tool_router: ToolRouter<Self>,
}

#[tool_router]
impl Tools {
    fn new() -> Self {
        Self {
            tool_router: Self::tool_router(),
        }
    }

    #[tool(
        description = "Check the authenticated Owner's Shelf MCP connection. Returns identity and expiry, never credentials.",
        annotations(
            read_only_hint = true,
            destructive_hint = false,
            idempotent_hint = true,
            open_world_hint = false
        )
    )]
    fn shelf_status(
        &self,
        Extension(parts): Extension<Parts>,
    ) -> Result<CallToolResult, ErrorData> {
        let identity = parts
            .extensions
            .get::<Identity>()
            .ok_or_else(|| ErrorData::internal_error("Authentication context is missing.", None))?;
        Ok(CallToolResult::structured(
            json!({"authenticated":true,"owner_github_id":identity.sub,"access_expires_at":identity.expires_at,"scope":identity.scope}),
        ))
    }
}

#[tool_handler(router = self.tool_router)]
impl ServerHandler for Tools {
    fn get_info(&self) -> ServerConfig {
        ServerConfig::new(ServerCapabilities::builder().enable_tools().build()).with_instructions("Shelf MCP auth base. Only shelf_status is available. Site changes are not implemented here yet.")
    }
}

#[derive(Clone)]
struct AppState {
    config: Settings,
    http: reqwest::Client,
}

fn router(config: Settings) -> Result<Router> {
    let state = Arc::new(AppState {
        config,
        http: auth::http_client()?,
    });
    let service = StreamableHttpService::new(
        || Ok(Tools::new()),
        LocalSessionManager::default().into(),
        StreamableHttpServerConfig::default()
            .with_legacy_session_mode(false)
            .with_json_response(true),
    );
    let protected = Router::new()
        .route_service("/mcp", service)
        .layer(middleware::from_fn_with_state(state.clone(), require_owner));
    Ok(Router::new()
        .route("/.well-known/oauth-protected-resource/mcp", get(metadata))
        .route("/.well-known/oauth-protected-resource", get(metadata))
        .merge(protected)
        .with_state(state.clone())
        .layer(tower_http::limit::RequestBodyLimitLayer::new(64 * 1024))
        .layer(middleware::from_fn_with_state(state, check_address)))
}

async fn metadata(State(state): State<Arc<AppState>>) -> Json<serde_json::Value> {
    Json(
        json!({"resource":state.config.resource,"authorization_servers":[state.config.issuer],"scopes_supported":["shelf:read"],"bearer_methods_supported":["header"]}),
    )
}

fn challenge(config: &Settings) -> Response {
    let mut response = (
        StatusCode::UNAUTHORIZED,
        Json(json!({"error":"invalid_token"})),
    )
        .into_response();
    let value = format!(
        "Bearer resource_metadata=\"{}\", scope=\"shelf:read\"",
        config.metadata_url()
    );
    if let Ok(value) = value.parse() {
        response
            .headers_mut()
            .insert(header::WWW_AUTHENTICATE, value);
    }
    response
}

async fn require_owner(
    State(state): State<Arc<AppState>>,
    mut request: Request,
    next: Next,
) -> Response {
    if request.method() == axum::http::Method::OPTIONS {
        return StatusCode::NO_CONTENT.into_response();
    }
    let headers = request.headers();
    if headers.get_all(header::AUTHORIZATION).iter().count() != 1 {
        return challenge(&state.config);
    }
    let Some(token) = headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.split_once(' '))
        .filter(|(scheme, _)| scheme.eq_ignore_ascii_case("Bearer"))
        .map(|(_, token)| token)
        .filter(|v| !v.is_empty() && v.len() <= 256)
    else {
        return challenge(&state.config);
    };
    match auth::verify(&state.config, &state.http, token).await {
        Ok(Some(identity)) => {
            request.extensions_mut().insert(identity);
            next.run(request).await
        }
        Ok(None) => challenge(&state.config),
        Err(_) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error":"authorization_unavailable"})),
        )
            .into_response(),
    }
}

async fn check_address(
    State(state): State<Arc<AppState>>,
    request: Request,
    next: Next,
) -> Response {
    let headers = request.headers();
    if headers.get_all(header::HOST).iter().count() != 1
        || !headers
            .get(header::HOST)
            .and_then(|v| v.to_str().ok())
            .is_some_and(|host| state.config.accepts_host(host))
    {
        return StatusCode::FORBIDDEN.into_response();
    }
    if headers.get_all(header::ORIGIN).iter().count() > 1 {
        return StatusCode::FORBIDDEN.into_response();
    }
    let origin = headers.get(header::ORIGIN).cloned();
    if origin.as_ref().is_some_and(|origin| {
        origin
            .to_str()
            .ok()
            .is_none_or(|o| !state.config.origins.iter().any(|allowed| allowed == o))
    }) {
        return StatusCode::FORBIDDEN.into_response();
    }
    let mut response = next.run(request).await;
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, "no-store".parse().unwrap());
    if let Some(origin) = origin {
        let headers = response.headers_mut();
        headers.insert(header::ACCESS_CONTROL_ALLOW_ORIGIN, origin);
        headers.insert(header::VARY, "Origin".parse().unwrap());
        headers.insert(
            header::ACCESS_CONTROL_ALLOW_METHODS,
            "POST, GET, DELETE, OPTIONS".parse().unwrap(),
        );
        headers.insert(
            header::ACCESS_CONTROL_ALLOW_HEADERS,
            "Authorization, Content-Type, Accept, MCP-Protocol-Version, MCP-Session-Id"
                .parse()
                .unwrap(),
        );
        headers.insert(
            header::ACCESS_CONTROL_EXPOSE_HEADERS,
            "WWW-Authenticate, MCP-Session-Id, MCP-Protocol-Version"
                .parse()
                .unwrap(),
        );
    }
    response
}

pub fn run() -> Result<()> {
    let config = Settings::load()?;
    let bind = config.bind;
    let runtime = tokio::runtime::Runtime::new()?;
    runtime.block_on(async move {
        let app = router(config)?;
        let listener = tokio::net::TcpListener::bind(bind).await?;
        eprintln!("Shelf MCP listening on {bind}. Only shelf_status is enabled.");
        axum::serve(listener, app)
            .with_graceful_shutdown(async {
                let _ = tokio::signal::ctrl_c().await;
            })
            .await?;
        Ok(())
    })
}
