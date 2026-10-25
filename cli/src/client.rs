//! One function for each API request. Answers are JSON. Every error has the
//! shape {"error": {"code": "...", "message": "..."}}.

use std::fmt;
use std::time::Duration;

use anyhow::{Context, Result};
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
    pub interval: u64,
}

/// A key from an approved login. Also the value in the key store.
#[derive(Serialize, Deserialize)]
pub struct NewKey {
    pub key: String,
    pub expires_at: i64, // Unix seconds
}

#[derive(Deserialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum Poll {
    Wait,
    SlowDown,
    Denied,
    Expired,
    Approved(NewKey),
}

#[derive(Deserialize)]
struct ErrorBody {
    error: ApiError,
}

#[derive(Debug, Deserialize)]
pub struct ApiError {
    #[serde(skip)]
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
            .post("/cli/login", None, json!({ "device_name": device_name }))?
            .json()?)
    }

    pub fn poll(&self, device_code: &str) -> Result<Poll> {
        Ok(self
            .post(
                "/cli/login/poll",
                None,
                json!({ "device_code": device_code }),
            )?
            .json()?)
    }

    pub fn logout(&self, key: &str) -> Result<()> {
        self.post("/cli/logout", Some(key), json!({}))?;
        Ok(())
    }

    fn post(&self, path: &str, key: Option<&str>, body: serde_json::Value) -> Result<Response> {
        let mut req = self
            .http
            .post(format!("{}/api/v1{path}", self.base))
            .json(&body);
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
