use std::{net::SocketAddr, time::Duration};

use anyhow::{Context, Result, bail};
use reqwest::Url;
use serde::{Deserialize, Serialize};

#[derive(Clone)]
pub(super) struct Settings {
    pub resource: String,
    pub issuer: String,
    pub owner: String,
    pub bind: SocketAddr,
    pub public_url: Url,
    pub origins: Vec<String>,
}

impl Settings {
    pub fn load() -> Result<Self> {
        let read = |key: &str, default: &str| std::env::var(key).unwrap_or_else(|_| default.into());
        Self::parse(
            &read("SHELF_MCP_URL", "http://localhost:8081/mcp"),
            &read("SHELF_MCP_BIND", "127.0.0.1:8081"),
            &read("SHELF_URL", "http://localhost:8080"),
            &read("OWNER_GITHUB_ID", ""),
            &read("SHELF_MCP_ALLOWED_ORIGINS", ""),
        )
    }

    pub fn parse(
        resource: &str,
        bind: &str,
        issuer: &str,
        owner: &str,
        origins: &str,
    ) -> Result<Self> {
        let public_url = safe_url(resource)?;
        if public_url.path() != "/mcp" {
            bail!("SHELF_MCP_URL must end in /mcp.");
        }
        let issuer_url = safe_url(issuer)?;
        if issuer_url.path() != "/" {
            bail!("SHELF_URL must be only an origin.");
        }
        if owner.parse::<u64>().ok().filter(|id| *id > 0).is_none() {
            bail!("Set OWNER_GITHUB_ID to the Owner's numeric GitHub ID.");
        }
        let bind: SocketAddr = bind
            .parse()
            .context("SHELF_MCP_BIND must be an IP address and port.")?;
        if public_url.scheme() == "http" && !bind.ip().is_loopback() {
            bail!("Local HTTP must bind to a loopback address.");
        }
        let mut allowed = vec![public_url.origin().ascii_serialization()];
        for origin in origins.split(',').filter(|s| !s.is_empty()) {
            let url = safe_url(origin.trim())?;
            if url.path() != "/" {
                bail!("Allowed origins must not have a path.");
            }
            allowed.push(url.origin().ascii_serialization());
        }
        Ok(Self {
            resource: public_url.to_string(),
            issuer: issuer_url.origin().ascii_serialization(),
            owner: owner.into(),
            bind,
            public_url,
            origins: allowed,
        })
    }

    pub fn accepts(&self, identity: &Identity) -> bool {
        identity.iss == self.issuer
            && identity.sub == self.owner
            && identity.aud == self.resource
            && identity.expires_at > crate::now()
            && identity
                .scope
                .split_ascii_whitespace()
                .any(|s| s == "shelf:read")
    }

    pub fn metadata_url(&self) -> String {
        format!(
            "{}/.well-known/oauth-protected-resource/mcp",
            self.public_url.origin().ascii_serialization()
        )
    }

    pub fn accepts_host(&self, host: &str) -> bool {
        let Ok(url) = Url::parse(&format!("{}://{host}", self.public_url.scheme())) else {
            return false;
        };
        url.username().is_empty()
            && url.password().is_none()
            && url.path() == "/"
            && url.query().is_none()
            && url.fragment().is_none()
            && url.host_str() == self.public_url.host_str()
            && url.port_or_known_default() == self.public_url.port_or_known_default()
    }
}

fn safe_url(raw: &str) -> Result<Url> {
    let url = Url::parse(raw).context("Invalid MCP or Shelf URL.")?;
    let loopback = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
    if url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || raw.contains('#')
        || !(url.scheme() == "https" || url.scheme() == "http" && loopback)
    {
        bail!(
            "Use HTTPS, or HTTP on a loopback host. URLs must not contain credentials, queries, or fragments."
        );
    }
    Ok(url)
}

#[derive(Clone, Deserialize, Serialize)]
pub(super) struct Identity {
    pub iss: String,
    pub sub: String,
    pub aud: String,
    pub scope: String,
    pub expires_at: i64,
}

pub(super) fn http_client() -> Result<reqwest::Client> {
    Ok(reqwest::Client::builder()
        .timeout(Duration::from_secs(10))
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .build()?)
}

// Tokens are opaque. Verify online with the configured issuer on every request,
// so revoked grants stop working without a local token-cache delay.
pub(super) async fn verify(
    config: &Settings,
    http: &reqwest::Client,
    token: &str,
) -> Result<Option<Identity>> {
    let mut endpoint = Url::parse(&format!("{}/oauth/session", config.issuer))?;
    endpoint
        .query_pairs_mut()
        .append_pair("resource", &config.resource);
    let mut response = http.get(endpoint).bearer_auth(token).send().await?;
    if matches!(response.status().as_u16(), 401 | 403) {
        return Ok(None);
    }
    if !response.status().is_success() {
        bail!("The authorization server is unavailable.");
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await? {
        if bytes.len() + chunk.len() > 8192 {
            bail!("The authorization response is too large.");
        }
        bytes.extend_from_slice(&chunk);
    }
    let identity: Identity =
        serde_json::from_slice(&bytes).context("Invalid authorization response.")?;
    Ok(config.accepts(&identity).then_some(identity))
}
