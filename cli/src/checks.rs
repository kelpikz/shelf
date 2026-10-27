//! Upload checks: which local files an upload sends, and what to fix first.
//! The server checks the same limits; the CLI finds them before a long upload.

use std::collections::HashSet;
use std::fs;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};

use crate::links;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    File,
    Folder,
}

pub struct Limits {
    pub bytes: u64,     // all files, unpacked
    pub entries: usize, // files and folders
}

/// The server's limits (server/internal/sites).
pub const LIMITS: Limits = Limits {
    bytes: 50 << 20,
    entries: 10_000,
};

/// Skipped at any depth, as are names that start with ".". __MACOSX comes
/// from zip files made on macOS.
const SKIP: [&str; 2] = ["node_modules", "__MACOSX"];

#[derive(Debug)]
pub struct Upload {
    pub kind: Kind,
    pub root: PathBuf,        // the folder; for one file, its parent
    pub files: Vec<String>,   // relative to root, "/"-separated, sorted
    pub bytes: u64,           // all files
    pub skipped: Vec<String>, // sorted; folders end in "/"
    pub warnings: Vec<String>,
    pub errors: Vec<String>, // an error stops the upload
}

pub fn check(path: &Path) -> Result<Upload> {
    check_with(path, &LIMITS)
}

fn check_with(path: &Path, limits: &Limits) -> Result<Upload> {
    // Follows a link: the Owner named this path.
    let meta = fs::metadata(path).with_context(|| format!("Cannot open {}", path.display()))?;
    let mut up = Upload {
        kind: Kind::Folder,
        root: path.to_path_buf(),
        files: Vec::new(),
        bytes: 0,
        skipped: Vec::new(),
        warnings: Vec::new(),
        errors: Vec::new(),
    };

    if meta.is_dir() {
        walk(path, "", limits, &mut up)?;
        up.files.sort();
        up.skipped.sort();
        // Exact case: the server does not find Index.html for /path/.
        if !up.files.iter().any(|f| f == "index.html") {
            up.errors
                .push("The folder has no index.html at its top.".into());
        }
        for f in &up.files {
            // A browser reads "#" as a fragment and "?" as a query, not as part of the name.
            if f.contains(['#', '?']) {
                up.warnings.push(format!(
                    "{f} has \"#\" or \"?\" in its name. A plain link to it breaks."
                ));
            }
        }
    } else if meta.is_file() {
        let name = path
            .file_name()
            .and_then(|n| n.to_str())
            .with_context(|| format!("{}: the name is not valid text.", path.display()))?;
        up.kind = Kind::File;
        up.root = path.parent().unwrap_or(Path::new("")).to_path_buf();
        up.bytes = meta.len();
        if name.starts_with('.') {
            up.errors
                .push(format!("{name} is a dotfile. Dotfiles are not uploaded."));
        }
        up.files.push(name.into());
    } else {
        bail!("{} is not a file or a folder.", path.display());
    }

    if up.files.len() + folders(&up.files) > limits.entries {
        up.errors
            .push(format!("More than {} files and folders.", limits.entries));
    }
    if up.bytes > limits.bytes {
        up.errors
            .push(format!("More than {} MB of files.", limits.bytes >> 20));
    }
    // Only within the limits: the checks read the files.
    if up.errors.is_empty() {
        up.warnings.extend(links::check(&up.root, &up.files)?);
    }
    Ok(up)
}

/// Adds the files under dir to up. prefix is dir relative to the root: "" or
/// "a/b/".
fn walk(dir: &Path, prefix: &str, limits: &Limits, up: &mut Upload) -> Result<()> {
    let entries = fs::read_dir(dir).with_context(|| format!("Cannot read {}", dir.display()))?;
    for entry in entries {
        // Over the limit on files alone: check_with reports it, so stop here
        // and do not walk a very large tree.
        if up.files.len() > limits.entries {
            return Ok(());
        }
        let entry = entry.with_context(|| format!("Cannot read {}", dir.display()))?;
        let raw = entry.file_name();
        let Some(name) = raw.to_str() else {
            up.errors.push(format!(
                "{prefix}{}: the name is not valid text.",
                raw.to_string_lossy()
            ));
            continue;
        };
        let rel = format!("{prefix}{name}");
        // DirEntry::file_type does not follow links (lstat, not stat), so a
        // link is neither a file nor a folder. On Windows, junctions too.
        let ft = entry
            .file_type()
            .with_context(|| format!("Cannot read {}", entry.path().display()))?;

        if name.starts_with('.') || SKIP.contains(&name) || !(ft.is_file() || ft.is_dir()) {
            up.skipped.push(if ft.is_dir() { rel + "/" } else { rel });
        } else if ft.is_dir() {
            walk(&entry.path(), &format!("{rel}/"), limits, up)?;
        } else {
            let meta = entry
                .metadata()
                .with_context(|| format!("Cannot read {}", entry.path().display()))?;
            up.bytes += meta.len();
            up.files.push(rel);
        }
    }
    Ok(())
}

/// The folders that the file names make: "a/b/c.txt" makes "a" and "a/b".
/// The server counts them toward the limit; empty folders are not sent.
fn folders(files: &[String]) -> usize {
    files
        .iter()
        .flat_map(|f| f.match_indices('/').map(|(i, _)| &f[..i]))
        .collect::<HashSet<_>>()
        .len()
}

#[cfg(test)]
mod tests {
    use std::fs;

    use tempfile::TempDir;

    use super::*;

    /// A temp folder with each file (content "x") and its parent folders.
    fn tree(files: &[&str]) -> TempDir {
        let dir = TempDir::new().unwrap();
        for f in files {
            let p = dir.path().join(f);
            fs::create_dir_all(p.parent().unwrap()).unwrap();
            fs::write(p, "x").unwrap();
        }
        dir
    }

    #[test]
    fn folder_skips_dotfiles_and_build_folders() {
        let dir = tree(&[
            "index.html",
            "css/site.css",
            "img/a.png",
            "img/.DS_Store",
            ".env",
            ".git/config",
            "node_modules/x/index.js",
            "__MACOSX/._a.png",
        ]);
        let up = check(dir.path()).unwrap();
        assert_eq!(up.kind, Kind::Folder);
        assert_eq!(up.root, dir.path());
        assert_eq!(up.files, ["css/site.css", "img/a.png", "index.html"]);
        assert_eq!(up.bytes, 3);
        assert_eq!(
            up.skipped,
            [
                ".env",
                ".git/",
                "__MACOSX/",
                "img/.DS_Store",
                "node_modules/"
            ]
        );
        assert!(up.warnings.is_empty());
        assert!(up.errors.is_empty());
    }

    #[test]
    fn folder_needs_index_html_at_its_top_with_the_exact_case() {
        let nested = tree(&["about/index.html"]);
        assert_eq!(check(nested.path()).unwrap().errors.len(), 1);
        let upper = tree(&["Index.html"]);
        assert_eq!(check(upper.path()).unwrap().errors.len(), 1);
    }

    #[test]
    fn one_file() {
        let dir = tree(&["notes.pdf", ".env"]);
        let up = check(&dir.path().join("notes.pdf")).unwrap();
        assert_eq!(up.kind, Kind::File);
        assert_eq!(up.root, dir.path());
        assert_eq!(up.files, ["notes.pdf"]);
        assert_eq!(up.bytes, 1);
        assert!(up.errors.is_empty());

        let dotfile = check(&dir.path().join(".env")).unwrap();
        assert_eq!(dotfile.errors.len(), 1);
    }

    #[test]
    fn limits_count_files_and_folders() {
        // index.html, a/, a/b/, a/b/c.txt: 4 entries, 2 bytes.
        let dir = tree(&["index.html", "a/b/c.txt"]);
        let at_limit = Limits {
            bytes: 2,
            entries: 4,
        };
        assert!(check_with(dir.path(), &at_limit).unwrap().errors.is_empty());
        let few = Limits {
            bytes: 2,
            entries: 3,
        };
        assert_eq!(check_with(dir.path(), &few).unwrap().errors.len(), 1);
        let small = Limits {
            bytes: 1,
            entries: 4,
        };
        assert_eq!(check_with(dir.path(), &small).unwrap().errors.len(), 1);
    }

    #[test]
    fn warns_about_hash_in_a_name() {
        // Windows does not allow "?" in a file name, so only "#" is tested.
        let dir = tree(&["index.html", "photo#1.png"]);
        let up = check(dir.path()).unwrap();
        assert_eq!(up.warnings.len(), 1);
        assert!(up.errors.is_empty());
    }

    #[test]
    fn folder_runs_the_link_checks() {
        let dir = tree(&["index.html"]);
        fs::write(dir.path().join("index.html"), "<img src=\"/a.png\">").unwrap();
        let up = check(dir.path()).unwrap();
        assert_eq!(up.warnings.len(), 1);
        assert!(up.warnings[0].starts_with("index.html:1: /a.png"));
    }

    #[test]
    fn skips_symlinks() {
        let dir = tree(&["index.html", "data/a.txt"]);
        let link = dir.path().join("link");
        #[cfg(unix)]
        let made = std::os::unix::fs::symlink(dir.path().join("data"), &link);
        #[cfg(windows)]
        let made = std::os::windows::fs::symlink_dir(dir.path().join("data"), &link);
        if made.is_err() {
            // Windows needs Developer Mode or admin rights for a symlink.
            eprintln!("skipped: cannot make a symlink on this computer");
            return;
        }
        let up = check(dir.path()).unwrap();
        assert_eq!(up.files, ["data/a.txt", "index.html"]);
        assert_eq!(up.skipped, ["link"]);
    }
}
