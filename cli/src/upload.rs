//! Upload one checked archive. The server owns path and replacement rules.

use std::io::{self, BufRead, IsTerminal, Write};
use std::path::Path;

use anyhow::{Context, Result, bail};

use crate::client::{Client, SiteKind, Visibility};
use crate::{checks, current_key, pack, report, size, ui};

pub fn run(server: &str, source: &Path, path: &str, yes: bool, allow_secrets: bool) -> Result<()> {
    let Some(key) = current_key(server)? else {
        bail!("You are not logged in to {server}. Run: shelf login");
    };
    ui::dim("Checking files...");
    let up = checks::check(source, allow_secrets)?;
    report(&up);
    if !up.errors.is_empty() {
        bail!("Fix the errors before the upload.");
    }
    if !up.warnings.is_empty() && !confirm("Continue with these warnings?", yes)? {
        ui::dim("Upload cancelled.");
        return Ok(());
    }
    ui::dim("Packing...");
    let archive = pack::pack(&up)?;
    let kind = match up.kind {
        checks::Kind::File => SiteKind::File,
        checks::Kind::Folder => SiteKind::Folder,
    };
    let files = match up.files.len() {
        1 => "1 file".to_owned(),
        n => format!("{n} files"),
    };
    ui::detail(format_args!(
        "{files}, {} ({} packed).",
        size(up.bytes),
        size(archive.len() as u64)
    ));
    drop(up);
    let client = Client::new(server)?;
    if let Some(site) = client.check_path(&key, path)? {
        ui::detail(format_args!(
            "Site {} is {}. Archived: {}.",
            site.path.escape_debug(),
            site.visibility.as_str(),
            site.archived
        ));
        if site.visibility == Visibility::Public {
            ui::warn("The replacement files will be Public too.");
        }
        if site.archived {
            ui::warn("The Site will stay Archived.");
        }
        if !confirm("Replace this Site?", yes)? {
            ui::dim("Upload cancelled.");
            return Ok(());
        }
    }
    ui::dim("Sending the archive and waiting for the server...");
    let site = client.upload(&key, path, kind, archive)?;
    ui::ok(format_args!("Uploaded {}", site.url.escape_debug()));
    ui::detail(format_args!(
        "Visibility: {}. Archived: {}.",
        site.visibility.as_str(),
        site.archived
    ));
    if site.visibility == Visibility::Public {
        ui::warn("Browsers may still show old files from their cache.");
    }
    Ok(())
}

fn confirm(question: &str, yes: bool) -> Result<bool> {
    confirm_with(
        question,
        yes,
        io::stdin().is_terminal(),
        io::stdin().lock(),
        io::stderr().lock(),
    )
}

fn confirm_with(
    question: &str,
    yes: bool,
    terminal: bool,
    mut input: impl BufRead,
    mut output: impl Write,
) -> Result<bool> {
    if yes {
        return Ok(true);
    }
    if !terminal {
        bail!("{question} A terminal is required. Use --yes to accept the prompts.");
    }
    write!(output, "{question} [y/N] ")?;
    output.flush()?;
    let mut answer = String::new();
    input
        .read_line(&mut answer)
        .context("Could not read the answer.")?;
    Ok(matches!(
        answer.trim().to_ascii_lowercase().as_str(),
        "y" | "yes"
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    #[test]
    fn confirmation_defaults_to_no_and_requires_explicit_yes() {
        for (answer, expected) in [
            ("", false),
            ("\n", false),
            ("no\n", false),
            ("other\n", false),
            ("y\n", true),
            ("YES\n", true),
        ] {
            assert_eq!(
                confirm_with("Replace?", false, true, Cursor::new(answer), Vec::new()).unwrap(),
                expected
            );
        }
        assert!(confirm_with("Replace?", false, false, Cursor::new("yes\n"), Vec::new()).is_err());
        assert!(confirm_with("Replace?", true, false, Cursor::new(""), Vec::new()).unwrap());
    }
}
