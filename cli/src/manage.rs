//! Site management commands. All writes use the CLI key.

use crate::{
    client::{Client, Site, SiteChange, Visibility},
    current_key, ui,
};
use anstream::println;
use anyhow::{Result, bail};
use std::io::{self, BufRead, Write};

fn connection(server: &str) -> Result<(Client, String)> {
    let Some(key) = current_key(server)? else {
        bail!("You are not logged in to {server}. Run: shelf login");
    };
    Ok((Client::new(server)?, key))
}

pub fn list(server: &str, all: bool) -> Result<()> {
    let (client, key) = connection(server)?;
    let sites = client.list_sites(&key, all)?;
    if sites.is_empty() {
        ui::dim("No Sites.");
        return Ok(());
    }
    let rows: Vec<_> = sites
        .iter()
        .map(|site| {
            let path = site.path.escape_debug().to_string();
            let visibility = format!(
                "{}{}",
                site.visibility.as_str(),
                if site.archived { " / archived" } else { "" }
            );
            (path, visibility, site)
        })
        .collect();
    let path_width = rows
        .iter()
        .map(|(path, _, _)| path.len())
        .max()
        .unwrap()
        .max(4);
    let visibility_width = rows
        .iter()
        .map(|(_, visibility, _)| visibility.len())
        .max()
        .unwrap()
        .max(10);
    let bold = ui::BOLD;
    println!(
        "{bold}{:<path_width$}  {:<visibility_width$}  URL{bold:#}",
        "SITE", "VISIBILITY"
    );
    for (path, visibility, site) in rows {
        let style = match site.visibility {
            Visibility::Public => ui::GOOD,
            Visibility::Private => ui::WARN,
        };
        println!(
            "{path:<path_width$}  {style}{visibility:<visibility_width$}{style:#}  {}",
            site.url.escape_debug()
        );
    }
    Ok(())
}

fn show(site: &Site) {
    println!(
        "{}  {}{}  {}",
        site.path.escape_debug(),
        site.visibility.as_str(),
        if site.archived { " / archived" } else { "" },
        site.url.escape_debug()
    );
}

pub fn change(server: &str, path: &str, change: SiteChange) -> Result<()> {
    let (client, key) = connection(server)?;
    let site = client.change_site(&key, path, change)?;
    ui::ok("Site updated.");
    show(&site);
    Ok(())
}

pub fn delete(server: &str, path: &str) -> Result<()> {
    let (client, key) = connection(server)?;
    let Some(site) = client.check_path(&key, path)? else {
        bail!("No Site at this path.");
    };
    show(&site);
    eprintln!("This deletes the Site and its files for ever.");
    eprint!("Type {} to delete it: ", path.escape_debug());
    io::stderr().flush()?;
    let mut answer = String::new();
    io::stdin().lock().read_line(&mut answer)?;
    if answer.trim_end_matches(['\r', '\n']) != path {
        ui::dim("Delete cancelled.");
        return Ok(());
    }
    client.delete_site(&key, path)?;
    ui::ok(format_args!("Deleted {}", path.escape_debug()));
    Ok(())
}
