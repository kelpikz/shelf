//! One function for each API request. Answers are JSON. Every error has the
//! shape {"error": {"code": "...", "message": "..."}}.

use std::fmt;
use std::time::Duration;

use anyhow::{Context, Result};
use reqwest::Method;
use reqwest::blocking::Response;
use serde::{Deserialize, Serialize};
use serde_json::json;

pub struct Client {
    base: String,
    http: reqwest::blocking::Client,
}

#[derive(Deserialize)]
pub struct LoginStart {
    pub device_code: String,
    pub user_code: String,
    pub verification_uri: String,
    pub interval: u64, // seconds between polls
}

/// A key from an approved login, as saved in the key store.
#[derive(Serialize, Deserialize)]
pub struct SavedKey {
    pub key: String,
    #[serde(default)] // keys saved before device names
    pub device_name: String,
    pub expires_at: i64, // Unix seconds
}

/// What the server knows about a key.
#[derive(Deserialize)]
pub struct KeyInfo {
    pub device_name: String,
    pub expires_at: i64, // Unix seconds
}

#[derive(Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum Poll {
    Wait,
    SlowDown,
    Denied,
    Expired,
    Approved(SavedKey),
}

#[derive(Deserialize)]
struct ErrorBody {
    error: ApiError,
}

#[derive(Debug, Deserialize)]
pub struct ApiError {
    #[serde(skip)] // from the HTTP status line, not the body
    pub status: u16,
    pub message: String,
}

impl fmt::Display for ApiError {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for ApiError {}

impl Client {
    pub fn new(base: &str) -> Result<Self> {
        let http = reqwest::blocking::Client::builder()
            .user_agent(concat!("shelf-cli/", env!("CARGO_PKG_VERSION")))
            .timeout(Duration::from_secs(30))
            .build()?;
        Ok(Self {
            base: base.to_string(),
            http,
        })
    }

    pub fn start_login(&self, device_name: &str) -> Result<LoginStart> {
        Ok(self
            .send(
                Method::POST,
                "/cli/login",
                None,
                Some(json!({ "device_name": device_name })),
            )?
            .json()?)
    }

    pub fn poll(&self, device_code: &str) -> Result<Poll> {
        Ok(self
            .send(
                Method::POST,
                "/cli/login/poll",
                None,
                Some(json!({ "device_code": device_code })),
            )?
            .json()?)
    }

    pub fn logout(&self, key: &str) -> Result<()> {
        self.send(Method::POST, "/cli/logout", Some(key), None)?;
        Ok(())
    }

    /// Asks the server about the key. A cancelled or ended key gives 401.
    pub fn key_info(&self, key: &str) -> Result<KeyInfo> {
        Ok(self
            .send(Method::GET, "/cli/key", Some(key), None)?
            .json()?)
    }

    /// Renames the key on the server.
    pub fn set_device_name(&self, key: &str, name: &str) -> Result<KeyInfo> {
        Ok(self
            .send(
                Method::PATCH,
                "/cli/key",
                Some(key),
                Some(json!({ "device_name": name })),
            )?
            .json()?)
    }

    fn send(
        &self,
        method: Method,
        path: &str,
        key: Option<&str>,
        body: Option<serde_json::Value>,
    ) -> Result<Response> {
        let mut req = self
            .http
            .request(method, format!("{}/api/v1{path}", self.base));
        if let Some(body) = body {
            req = req.json(&body);
        }
        if let Some(key) = key {
            req = req.bearer_auth(key);
        }
        let resp = req
            .send()
            .with_context(|| format!("Could not reach {}", self.base))?;
        let status = resp.status();
        if status.is_success() {
            return Ok(resp);
        }
        let mut err = resp
            .json::<ErrorBody>()
            .map(|b| b.error)
            .unwrap_or_else(|_| ApiError {
                status: 0,
                message: format!("The server answered with HTTP {}.", status.as_u16()),
            });
        err.status = status.as_u16();
        Err(err.into())
    }
}

/// True if the server refused the key: it was cancelled, or it ended.
pub fn is_unauthorized(err: &anyhow::Error) -> bool {
    err.downcast_ref::<ApiError>()
        .is_some_and(|e| e.status == 401)
}

#[cfg(test)]
mod tests {
    use super::{Poll, SavedKey};

    #[test]
    fn approved_poll_has_the_device_name() {
        let poll: Poll = serde_json::from_str(
            r#"{"status":"approved","key":"shelf_x","device_name":"Work laptop","expires_at":1}"#,
        )
        .unwrap();
        assert!(matches!(poll, Poll::Approved(k) if k.device_name == "Work laptop"));
    }

    #[test]
    fn a_key_saved_before_device_names_still_loads() {
        let key: SavedKey = serde_json::from_str(r#"{"key":"shelf_x","expires_at":1}"#).unwrap();
        assert_eq!(key.device_name, "");
    }
}
