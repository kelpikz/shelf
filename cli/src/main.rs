mod checks;
mod client;
mod keystore;
mod links;
mod login;
mod pack;
mod secrets;
mod ui;
mod upload;

use std::env;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use anstream::println;
use anyhow::{Context, Result, bail};
use clap::{Parser, Subcommand};
use reqwest::Url;

use client::Client;
use ui::{BOLD, CODE, LINK};

const DEFAULT_SERVER: &str = "https://files-admin.ajitha.fyi";

#[derive(Parser)]
#[command(version, about = "Upload files and folders to shelf.", styles = ui::HELP)]
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
    /// Change a setting of this computer's key
    Set {
        /// The device name on the Admin page, for example "Work laptop"
        #[arg(long)]
        name: String,
    },
    /// Run the Upload checks on a file or a folder. Sends nothing.
    Check {
        /// The file or the folder to upload
        path: PathBuf,
        /// Show found secrets as warnings, not errors
        #[arg(long)]
        allow_secrets: bool,
    },
    /// Upload a file or a folder to a Site
    Upload {
        /// The local file or folder
        source: PathBuf,
        /// The Site path, for example notes
        path: String,
        /// Accept warning and replacement prompts
        #[arg(long)]
        yes: bool,
        /// Show found secrets as warnings, not errors
        #[arg(long)]
        allow_secrets: bool,
    },
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let result = server_url(env::var("SHELF_URL").ok()).and_then(|server| match cli.command {
        Command::Login => login(&server),
        Command::Logout => logout(&server),
        Command::Status => status(&server),
        Command::Set { name } => set_name(&server, &name),
        Command::Check {
            path,
            allow_secrets,
        } => check(&path, allow_secrets),
        Command::Upload {
            source,
            path,
            yes,
            allow_secrets,
        } => upload::run(&server, &source, &path, yes, allow_secrets),
    });
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(e) => {
            ui::error(&e);
            ExitCode::FAILURE
        }
    }
}

/// SHELF_URL, or the default server. The key goes over this connection, so
/// plain http is only for local dev. The parsed host is checked, not the
/// text: in "http://localhost:80@remote.example" the host is remote.example.
fn server_url(from_env: Option<String>) -> Result<String> {
    let raw = from_env.unwrap_or_else(|| DEFAULT_SERVER.into());
    let url = Url::parse(&raw).context("SHELF_URL is not a valid address.")?;
    let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1"));
    match url.scheme() {
        "https" => {}
        "http" if local => {}
        _ => bail!("SHELF_URL must start with https://. Only localhost can use http://."),
    }
    if !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        bail!("SHELF_URL must be only the server address, for example {DEFAULT_SERVER}");
    }
    Ok(url.origin().ascii_serialization())
}

/// SHELF_KEY overrides the key store, for computers with no key store.
fn env_key() -> Option<String> {
    env::var("SHELF_KEY").ok().filter(|k| !k.is_empty())
}

fn login(server: &str) -> Result<()> {
    let client = Client::new(server)?;
    let device = gethostname::gethostname().to_string_lossy().into_owned();
    let start = client.start_login(&device)?;

    println!("Open this page and type the code:");
    println!();
    println!("  {LINK}{}{LINK:#}", start.verification_uri);
    println!("  Code: {CODE}{}{CODE:#}", start.user_code);
    println!();
    // Detached: some Linux launchers block until they exit, and the poll must
    // run. Over SSH there is no browser; the address is on the screen.
    let _ = open::that_detached(&start.verification_uri);
    ui::dim("Approve the login on that page. The CLI waits for you.");

    let key = login::wait_for_key(
        Duration::from_secs(start.interval),
        || client.poll(&start.device_code),
        thread::sleep,
    )?;
    let old = keystore::load(server).ok().flatten();
    if let Err(e) = keystore::save(server, &key) {
        // The server gives a key only once, and it stays valid until expires_at.
        show_key_once(&key.key);
        return Err(e);
    }
    if let Some(old) = old {
        // The new key replaces it; a failure leaves it to end on its own.
        let _ = client.logout(&old.key);
    }

    ui::ok(logged_in(server, &key.device_name));
    ui::detail(ends_in(key.expires_at - now()));
    if env_key().is_some() {
        ui::warn("SHELF_KEY is set. The CLI uses it in place of this key.");
    }
    Ok(())
}

/// For a computer with no key store, for example Linux over SSH.
fn show_key_once(key: &str) {
    ui::warn("The key store does not work. The CLI shows your key one time only:");
    println!();
    println!("  {CODE}{key}{CODE:#}");
    println!();
    ui::detail("Put it in the SHELF_KEY environment variable. Then shelf uses it.");
}

fn logout(server: &str) -> Result<()> {
    let from_env = env_key();
    let key = match &from_env {
        Some(key) => key.clone(),
        None => match keystore::load(server)? {
            Some(saved) => saved.key,
            None => {
                ui::warn(format_args!(
                    "You are not logged in to {BOLD}{server}{BOLD:#}."
                ));
                return Ok(());
            }
        },
    };

    match Client::new(server)?.logout(&key) {
        Ok(()) => {}
        Err(e) if client::is_unauthorized(&e) => {} // cancelled or ended already
        Err(e) => {
            return Err(e.context(
                "Could not cancel the key on the server. The key stays on this computer.",
            ));
        }
    }
    if from_env.is_none() {
        keystore::delete(server)?;
    }
    ui::ok(format_args!("Logged out of {BOLD}{server}{BOLD:#}"));
    if from_env.is_some() {
        ui::warn("Remove SHELF_KEY from your environment.");
    }
    Ok(())
}

/// The server checks the key each time: the Owner can cancel it on the Admin
/// page, and the saved copy does not know.
fn status(server: &str) -> Result<()> {
    let Some(key) = current_key(server)? else {
        ui::bad(format_args!("Not logged in to {BOLD}{server}{BOLD:#}"));
        ui::hint("shelf login");
        return Ok(());
    };
    match Client::new(server)?.key_info(&key) {
        Ok(info) => {
            ui::ok(logged_in(server, &info.device_name));
            ui::detail(ends_in(info.expires_at - now()));
            if env_key().is_some() {
                ui::detail("With SHELF_KEY.");
            }
        }
        Err(e) if client::is_unauthorized(&e) => {
            ui::bad(format_args!("Not logged in to {BOLD}{server}{BOLD:#}"));
            ui::detail("The key was cancelled, or it ended.");
            ui::hint("shelf login");
        }
        Err(e) => return Err(e.context("Could not check the key.")),
    }
    Ok(())
}

fn set_name(server: &str, name: &str) -> Result<()> {
    let Some(key) = current_key(server)? else {
        bail!("You are not logged in to {server}. Run: shelf login");
    };
    let info = Client::new(server)?.set_device_name(&key, name)?;
    ui::ok(format_args!(
        "The device name is now {BOLD}{}{BOLD:#}",
        info.device_name
    ));
    Ok(())
}

fn check(path: &Path, allow_secrets: bool) -> Result<()> {
    let up = checks::check(path, allow_secrets)?;
    report(&up);
    if !up.errors.is_empty() {
        bail!("Fix the errors before the upload.");
    }
    let packed = pack::pack(&up)?;
    let files = match up.files.len() {
        1 => "1 file".to_string(),
        n => format!("{n} files"),
    };
    ui::ok(format_args!(
        "{files}, {} ({} packed). Ready to upload.",
        size(up.bytes),
        size(packed.len() as u64)
    ));
    Ok(())
}

/// Prints the result of the Upload checks.
fn report(up: &checks::Upload) {
    if !up.skipped.is_empty() {
        ui::dim("Not uploaded (dotfiles, node_modules, __MACOSX, and links):");
        for name in &up.skipped {
            ui::detail(name);
        }
    }
    for w in &up.warnings {
        ui::warn(w);
    }
    for e in &up.errors {
        ui::bad(e);
    }
}

/// "512 bytes", "12 KB", "3.4 MB".
fn size(bytes: u64) -> String {
    const KB: u64 = 1024;
    const MB: u64 = 1024 * KB;
    if bytes < KB {
        format!("{bytes} bytes")
    } else if bytes < MB {
        format!("{} KB", bytes.div_ceil(KB))
    } else {
        format!("{:.1} MB", bytes as f64 / MB as f64)
    }
}

/// SHELF_KEY, or the saved key.
fn current_key(server: &str) -> Result<Option<String>> {
    match env_key() {
        Some(key) => Ok(Some(key)),
        None => Ok(keystore::load(server)?.map(|saved| saved.key)),
    }
}

/// "Logged in to <server> as <name>". Keys saved before device names have no name.
fn logged_in(server: &str, device_name: &str) -> String {
    let mut msg = format!("Logged in to {BOLD}{server}{BOLD:#}");
    if !device_name.is_empty() {
        msg += &format!(" as {BOLD}{device_name}{BOLD:#}");
    }
    msg
}

/// Rounds to the nearest day, so a new 30-day key shows 30, not 29.
fn ends_in(secs_left: i64) -> String {
    const DAY: i64 = 86_400;
    if secs_left < DAY {
        return "The key ends in less than one day.".into();
    }
    match (secs_left + DAY / 2) / DAY {
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
    use super::{ends_in, server_url};

    #[test]
    fn ends_in_rounds_to_the_nearest_day() {
        const DAY: i64 = 86_400;
        assert_eq!(ends_in(30 * DAY - 5), "The key ends in 30 days.");
        assert_eq!(ends_in(29 * DAY + DAY / 3), "The key ends in 29 days.");
        assert_eq!(ends_in(DAY + 100), "The key ends in 1 day.");
        assert_eq!(ends_in(DAY / 2), "The key ends in less than one day.");
    }

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
        // The host is remote.example; "localhost:80" is a user name and password.
        assert!(server_url(Some("http://localhost:80@remote.example".into())).is_err());
        assert!(server_url(Some("https://user:pw@files-admin.ajitha.fyi".into())).is_err());
        assert!(server_url(Some("https://files-admin.ajitha.fyi/api?x=1".into())).is_err());
        assert!(server_url(Some("files-admin.ajitha.fyi".into())).is_err());
    }
}
