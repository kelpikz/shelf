mod client;
mod keystore;
mod login;

use std::env;
use std::process::ExitCode;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anyhow::{Result, bail};
use clap::{Parser, Subcommand};

use client::Client;

const DEFAULT_SERVER: &str = "https://files-admin.ajitha.fyi";

#[derive(Parser)]
#[command(version, about = "Upload files and folders to shelf.")]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Log in through the browser
    Login,
    /// Cancel the key and delete it from this computer
    Logout,
    /// Show if you are logged in, and when the key ends
    Status,
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let result = server_url(env::var("SHELF_URL").ok()).and_then(|server| match cli.command {
        Command::Login => login(&server),
        Command::Logout => logout(&server),
        Command::Status => status(&server),
    });
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            eprintln!("Error: {e:#}");
            ExitCode::FAILURE
        }
    }
}

/// SHELF_URL, or the default server. The key goes over this connection, so
/// plain http is only for local dev.
fn server_url(from_env: Option<String>) -> Result<String> {
    let url = from_env.unwrap_or_else(|| DEFAULT_SERVER.into());
    let url = url.trim_end_matches('/').to_string();
    let local = ["http://localhost", "http://127.0.0.1"]
        .iter()
        .any(|p| url == *p || url.starts_with(&format!("{p}:")));
    if !url.starts_with("https://") && !local {
        bail!("SHELF_URL must start with https://. Only localhost can use http://.");
    }
    Ok(url)
}

/// SHELF_KEY overrides the key store, for computers with no key store.
fn env_key() -> Option<String> {
    env::var("SHELF_KEY").ok().filter(|k| !k.is_empty())
}

fn login(server: &str) -> Result<()> {
    let client = Client::new(server)?;
    let device = gethostname::gethostname().to_string_lossy().into_owned();
    let start = client.start_login(&device)?;

    println!("Open this page and type the code:\n");
    println!("  {}", start.verification_uri);
    println!("  Code: {}\n", start.user_code);
    // Over SSH there is no browser; the address is on the screen.
    let _ = open::that(&start.verification_uri);
    println!("Waiting for approval...");

    let key = login::wait_for_key(
        Duration::from_secs(start.interval),
        || client.poll(&start.device_code),
        thread::sleep,
    )?;
    let old = keystore::load(server).ok().flatten();
    keystore::save(server, &key)?;
    if let Some(old) = old {
        // The new key replaces it; a failure leaves it to end on its own.
        let _ = client.logout(&old.key);
    }

    println!("Logged in to {server}. {}", ends_in(key.expires_at));
    if env_key().is_some() {
        println!("Note: SHELF_KEY is set, and the CLI uses it in place of this key.");
    }
    Ok(())
}

fn logout(server: &str) -> Result<()> {
    let from_env = env_key();
    let key = match &from_env {
        Some(key) => key.clone(),
        None => match keystore::load(server)? {
            Some(saved) => saved.key,
            None => {
                println!("You are not logged in to {server}.");
                return Ok(());
            }
        },
    };

    match Client::new(server)?.logout(&key) {
        Ok(()) => {}
        Err(e) if client::is_unauthorized(&e) => {} // cancelled or ended already
        Err(e) => {
            return Err(
                e.context("Could not cancel the key on the server. The key stays on this computer")
            );
        }
    }
    if from_env.is_some() {
        println!("Logged out. Remove SHELF_KEY from your environment.");
    } else {
        keystore::delete(server)?;
        println!("Logged out of {server}.");
    }
    Ok(())
}

fn status(server: &str) -> Result<()> {
    if env_key().is_some() {
        println!("Logged in to {server} with SHELF_KEY.");
        return Ok(());
    }
    match keystore::load(server)? {
        None => println!("Not logged in to {server}. Run: shelf login"),
        Some(saved) if saved.expires_at <= now() => {
            println!("The key for {server} ended. Run: shelf login")
        }
        Some(saved) => println!("Logged in to {server}. {}", ends_in(saved.expires_at)),
    }
    Ok(())
}

fn ends_in(expires_at: i64) -> String {
    match (expires_at - now()) / 86_400 {
        0 => "The key ends in less than one day.".into(),
        1 => "The key ends in 1 day.".into(),
        days => format!("The key ends in {days} days."),
    }
}

fn now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::server_url;

    #[test]
    fn server_url_needs_https_except_for_localhost() {
        assert_eq!(server_url(None).unwrap(), "https://files-admin.ajitha.fyi");
        assert_eq!(
            server_url(Some("http://localhost:8080/".into())).unwrap(),
            "http://localhost:8080"
        );
        assert!(server_url(Some("http://127.0.0.1".into())).is_ok());
        assert!(server_url(Some("http://files-admin.ajitha.fyi".into())).is_err());
        assert!(server_url(Some("http://localhost.example.com".into())).is_err());
    }
}
