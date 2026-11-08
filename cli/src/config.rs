//! Local CLI settings in <OS config dir>/shelf/config.json.

use std::fmt;
use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};

#[derive(Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Config {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub server: Option<String>,
}

/// Where the active server address comes from.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Source {
    Env,
    Config,
    Default,
}

impl fmt::Display for Source {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        f.write_str(match self {
            Source::Env => "SHELF_URL",
            Source::Config => "config",
            Source::Default => "default",
        })
    }
}

pub fn path() -> Result<PathBuf> {
    let dir = dirs::config_dir().context("Could not find the config folder of this computer.")?;
    Ok(dir.join("shelf").join("config.json"))
}

/// A missing file is an empty config.
pub fn load(path: &Path) -> Result<Config> {
    match fs::read_to_string(path) {
        Ok(text) => serde_json::from_str(&text)
            .with_context(|| format!("The config file is not valid: {}", path.display())),
        Err(e) if e.kind() == ErrorKind::NotFound => Ok(Config::default()),
        Err(e) => Err(e).with_context(|| format!("Could not read {}", path.display())),
    }
}

pub fn save(path: &Path, config: &Config) -> Result<()> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir).with_context(|| format!("Could not create {}", dir.display()))?;
    }
    let text = serde_json::to_string_pretty(config)? + "\n";
    fs::write(path, text).with_context(|| format!("Could not write {}", path.display()))
}

/// SHELF_URL wins over the config file. None means the default server.
pub fn pick(env: Option<String>, saved: Option<String>) -> (Option<String>, Source) {
    match (env, saved) {
        (Some(url), _) => (Some(url), Source::Env),
        (None, Some(url)) => (Some(url), Source::Config),
        (None, None) => (None, Source::Default),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn save_then_load_keeps_the_server() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("shelf").join("config.json");
        assert_eq!(load(&path).unwrap(), Config::default());

        let config = Config {
            server: Some("http://localhost:8080".into()),
        };
        save(&path, &config).unwrap();
        assert_eq!(load(&path).unwrap(), config);

        save(&path, &Config::default()).unwrap();
        assert_eq!(load(&path).unwrap().server, None);
    }

    #[test]
    fn env_wins_then_config_then_default() {
        let env = || Some("https://env.example".to_string());
        let saved = || Some("https://saved.example".to_string());
        assert_eq!(pick(env(), saved()), (env(), Source::Env));
        assert_eq!(pick(None, saved()), (saved(), Source::Config));
        assert_eq!(pick(None, None), (None, Source::Default));
    }
}
