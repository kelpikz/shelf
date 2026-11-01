//! One function for each API request. Answers are JSON. Every error has the
//! shape {"error": {"code": "...", "message": "..."}}.

use std::fmt;
use std::time::Duration;

use anyhow::{Context, Result, bail};
use reqwest::blocking::Response;
use reqwest::{Method, Url};
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
    #[serde(default)]
    pub code: String,
    pub message: String,
}

#[derive(Debug, Clone, Copy, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SiteKind {
    File,
    Folder,
}

impl SiteKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::File => "file",
            Self::Folder => "folder",
        }
    }
}

#[derive(Debug, Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Visibility {
    Public,
    Private,
}

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum SiteChange {
    Visibility(Visibility),
    Archived(bool),
}

impl SiteChange {
    fn matches(self, site: &Site) -> bool {
        match self {
            Self::Visibility(value) => site.visibility == value,
            Self::Archived(value) => site.archived == value,
        }
    }
}

impl Visibility {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Public => "public",
            Self::Private => "private",
        }
    }
}

#[derive(Debug, Deserialize)]
pub struct Site {
    pub path: String,
    pub kind: SiteKind,
    pub file: String,
    pub visibility: Visibility,
    pub archived: bool,
    pub updated_at: i64,
    pub url: String,
}

impl Site {
    fn validate(&self, path: &str) -> Result<()> {
        validate_path(path)?;
        let url = Url::parse(&self.url).context("The Site URL is not valid.")?;
        let expected = match self.kind {
            SiteKind::File => format!("/{path}"),
            SiteKind::Folder => format!("/{path}/"),
        };
        if self.path != path
            || self.updated_at < 0
            || url.path() != expected
            || !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
            || (self.kind == SiteKind::File && (self.file.is_empty() || self.file.contains('/')))
            || (self.kind == SiteKind::Folder && !self.file.is_empty())
        {
            bail!("The server returned an unexpected Site.");
        }
        Ok(())
    }
}

#[derive(Deserialize)]
struct PathCheck {
    taken: bool,
    site: Option<Site>,
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
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
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

    fn site_url(&self, path: &str) -> Result<Url> {
        validate_path(path)?;
        let mut url = Url::parse(&format!("{}/api/v1/sites/", self.base))?;
        url.path_segments_mut()
            .map_err(|_| anyhow::anyhow!("The server URL is not valid."))?
            .pop_if_empty()
            .push(path);
        Ok(url)
    }

    pub fn check_path(&self, key: &str, path: &str) -> Result<Option<Site>> {
        let resp = self
            .http
            .get(self.site_url(path)?)
            .bearer_auth(key)
            .send()
            .context("Could not check the Site path.")?;
        let check: PathCheck = checked_response(resp)?
            .json()
            .context("The path-check response is not valid.")?;
        if check.taken != check.site.is_some() {
            bail!("The path-check response is not valid.");
        }
        if let Some(site) = &check.site {
            site.validate(path)?;
        }
        Ok(check.site)
    }

    pub fn list_sites(&self, key: &str, all: bool) -> Result<Vec<Site>> {
        let path = if all { "/sites?all=true" } else { "/sites" };
        let sites: Vec<Site> = self.send(Method::GET, path, Some(key), None)?.json()?;
        for site in &sites {
            site.validate(&site.path)?;
        }
        Ok(sites)
    }

    pub fn change_site(&self, key: &str, path: &str, change: SiteChange) -> Result<Site> {
        let response = self
            .http
            .patch(self.site_url(path)?)
            .bearer_auth(key)
            .json(&change)
            .send()
            .context(UNKNOWN_CHANGE)?;
        let response = checked_write_response(response, 200, UNKNOWN_CHANGE)?;
        let site: Site = response.json().context(UNKNOWN_CHANGE)?;
        site.validate(path).context(UNKNOWN_CHANGE)?;
        if !change.matches(&site) {
            bail!(UNKNOWN_CHANGE);
        }
        Ok(site)
    }

    pub fn delete_site(&self, key: &str, path: &str) -> Result<()> {
        let response = self
            .http
            .delete(self.site_url(path)?)
            .bearer_auth(key)
            .send()
            .context(UNKNOWN_CHANGE)?;
        checked_write_response(response, 204, UNKNOWN_CHANGE)?;
        Ok(())
    }

    pub fn upload(&self, key: &str, path: &str, kind: SiteKind, archive: Vec<u8>) -> Result<Site> {
        let mut url = self.site_url(path)?;
        url.query_pairs_mut().append_pair("kind", kind.as_str());
        let resp = self
            .http
            .put(url)
            .bearer_auth(key)
            .header("Content-Type", "application/gzip")
            .timeout(Duration::from_secs(180))
            .body(archive)
            .send()
            .context(UNKNOWN_UPLOAD)?;
        let resp = checked_write_response(resp, 200, UNKNOWN_UPLOAD)?;
        let site: Site = resp.json().context(UNKNOWN_UPLOAD)?;
        site.validate(path).context(UNKNOWN_UPLOAD)?;
        if site.kind != kind {
            bail!(UNKNOWN_UPLOAD);
        }
        Ok(site)
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
        checked_response(resp)
    }
}

const UNKNOWN_UPLOAD: &str = "The upload result is unknown. The Site may already have changed. Check it before trying again. The upload was not repeated.";
const UNKNOWN_CHANGE: &str = "The result is unknown. The Site may already have changed. Check it before trying again. The request was not repeated.";

fn checked_write_response(
    resp: Response,
    expected: u16,
    unknown: &'static str,
) -> Result<Response> {
    let resp = checked_response(resp).map_err(|err| {
        let refused = err.downcast_ref::<ApiError>().is_some_and(|e| {
            (400..500).contains(&e.status) && e.status != 408
                || (e.status == 503 && e.code == "upload_busy")
                || (e.status == 507 && e.code == "storage_full")
        });
        if refused { err } else { err.context(unknown) }
    })?;
    if resp.status().as_u16() != expected {
        bail!(unknown);
    }
    Ok(resp)
}

fn validate_path(path: &str) -> Result<()> {
    if path.is_empty()
        || path.len() > 64
        || path.starts_with('-')
        || !path
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-')
    {
        bail!(
            "A Site path has 1 to 64 lower case letters, digits, and '-', and does not start with '-'."
        );
    }
    Ok(())
}

fn checked_response(resp: Response) -> Result<Response> {
    let status = resp.status();
    if status.is_success() {
        return Ok(resp);
    }
    let mut err = resp
        .json::<ErrorBody>()
        .map(|b| b.error)
        .unwrap_or_else(|_| ApiError {
            status: 0,
            code: String::new(),
            message: format!("The server answered with HTTP {}.", status.as_u16()),
        });
    err.status = status.as_u16();
    Err(err.into())
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
