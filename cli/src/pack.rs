//! Packs the files of an upload into one .tar.gz, the body of the upload
//! request.

use anyhow::{Context, Result, bail};
use flate2::Compression;
use flate2::write::GzEncoder;
use tar::{Builder, EntryType, Header};

use crate::checks::Upload;

/// The server's limit for the .tar.gz (server/internal/sites).
pub const MAX_UPLOAD: usize = 10 << 20;

/// Packs the bytes that the checks read, not the files on the disk. Only for
/// an upload with no errors.
pub fn pack(up: &Upload) -> Result<Vec<u8>> {
    pack_with(up, MAX_UPLOAD)
}

fn pack_with(up: &Upload, max: usize) -> Result<Vec<u8>> {
    let too_large = || {
        format!(
            "The upload is more than {} MB after compression.",
            max >> 20
        )
    };
    let mut tar = Builder::new(GzEncoder::new(Vec::new(), Compression::default()));
    for (name, data) in up.files.iter().zip(&up.data) {
        // Only name, size, and mode: no owner, time, or other local data.
        let mut header = Header::new_gnu();
        header.set_entry_type(EntryType::Regular);
        header.set_size(data.len() as u64);
        header.set_mode(0o644);
        // name uses "/" on all systems; tar writes it as it is.
        tar.append_data(&mut header, name, data.as_slice())
            .with_context(|| format!("Cannot pack {name}"))?;
        // Stop early; the encoder may still hold some bytes.
        if tar.get_ref().get_ref().len() > max {
            bail!(too_large());
        }
    }
    let gz = tar.into_inner()?.finish()?;
    if gz.len() > max {
        bail!(too_large());
    }
    Ok(gz)
}

#[cfg(test)]
mod tests {
    use std::fs;
    use std::io::Read;

    use flate2::read::GzDecoder;
    use tempfile::TempDir;

    use super::*;
    use crate::checks;

    fn site() -> TempDir {
        let dir = TempDir::new().unwrap();
        fs::create_dir(dir.path().join("css")).unwrap();
        fs::write(dir.path().join("index.html"), "<h1>hi</h1>").unwrap();
        fs::write(dir.path().join("css").join("site.css"), "body {}").unwrap();
        dir
    }

    #[test]
    fn pack_keeps_the_names_with_slashes_and_the_checked_bytes() {
        let dir = site();
        let up = checks::check(dir.path(), false).unwrap();
        // A change after the checks does not get into the upload unchecked.
        fs::write(dir.path().join("index.html"), "changed").unwrap();
        let gz = pack(&up).unwrap();

        let mut archive = tar::Archive::new(GzDecoder::new(&gz[..]));
        let mut got = Vec::new();
        for entry in archive.entries().unwrap() {
            let mut entry = entry.unwrap();
            // The raw name, as the server reads it: "/" also on Windows.
            let name = String::from_utf8(entry.path_bytes().into_owned()).unwrap();
            let mut body = String::new();
            entry.read_to_string(&mut body).unwrap();
            got.push((name, body));
        }
        assert_eq!(
            got,
            [
                ("css/site.css".to_string(), "body {}".to_string()),
                ("index.html".to_string(), "<h1>hi</h1>".to_string()),
            ]
        );
    }

    #[test]
    fn pack_stops_over_the_limit() {
        let dir = site();
        let up = checks::check(dir.path(), false).unwrap();
        assert!(pack_with(&up, 10).is_err());
    }
}
