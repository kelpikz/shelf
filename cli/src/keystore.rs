//! The CLI key in the system key store: Windows Credential Manager, macOS
//! Keychain, or Linux Secret Service. One entry for each server address.

use anyhow::{Context, Result};
use keyring::{Entry, Error};

use crate::client::SavedKey;

const SERVICE: &str = "shelf";

fn entry(server: &str) -> Result<Entry> {
    Entry::new(SERVICE, server).context("Could not open the system key store")
}

pub fn load(server: &str) -> Result<Option<SavedKey>> {
    match entry(server)?.get_password() {
        Ok(json) => Ok(Some(
            serde_json::from_str(&json).context("The saved key is not valid. Run: shelf login")?,
        )),
        Err(Error::NoEntry) => Ok(None),
        Err(e) => Err(e).context("Could not read the key from the system key store"),
    }
}

pub fn save(server: &str, key: &SavedKey) -> Result<()> {
    entry(server)?
        .set_password(&serde_json::to_string(key)?)
        .context("Could not save the key in the system key store")
}

pub fn delete(server: &str) -> Result<()> {
    match entry(server)?.delete_credential() {
        Ok(()) | Err(Error::NoEntry) => Ok(()),
        Err(e) => Err(e).context("Could not delete the key from the system key store"),
    }
}
