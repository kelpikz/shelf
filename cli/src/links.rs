//! Link checks for HTML and CSS. A link that starts with "/" goes to the top of
//! the domain, not to the Site, and a link to a file that is not in the upload
//! breaks. Warnings only: links that JavaScript makes are not seen.

use std::collections::HashSet;
use std::fs;
use std::path::Path;
use std::sync::LazyLock;

use anyhow::{Context, Result};
use html5gum::{DefaultEmitter, Token, Tokenizer};
use regex::Regex;

/// url(...) in CSS: double quotes, single quotes, or none.
static CSS_URL: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)"'\s]*))\s*\)"#).unwrap()
});

/// A scheme, as in "https:" or "mailto:" (RFC 3986, section 3.1).
static SCHEME: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^[A-Za-z][A-Za-z0-9+.-]*:").unwrap());

/// Warnings for the links in the .html, .htm, and .css files of an upload.
pub fn check(root: &Path, files: &[String]) -> Result<Vec<String>> {
    let names: HashSet<&str> = files.iter().map(String::as_str).collect();
    let mut warnings = Vec::new();
    for f in files {
        let ext = f.rsplit_once('.').map(|(_, e)| e.to_ascii_lowercase());
        let links = match ext.as_deref() {
            Some("html" | "htm") => html_links(&read(root, f)?),
            Some("css") => css_links(&read(root, f)?, 1),
            _ => continue,
        };
        for (line, link) in links {
            if let Some(p) = problem(f, &link, &names) {
                warnings.push(format!("{f}:{line}: {p}"));
            }
        }
    }
    Ok(warnings)
}

fn read(root: &Path, name: &str) -> Result<String> {
    let bytes = fs::read(root.join(name)).with_context(|| format!("Cannot read {name}"))?;
    Ok(String::from_utf8_lossy(&bytes).into_owned())
}

/// (line, link) for each src and href, and each url(...) in a <style> block.
fn html_links(html: &str) -> Vec<(usize, String)> {
    let mut emitter = DefaultEmitter::<usize>::new_with_span();
    // Reads <script> and <style> content as text, as a browser does, so
    // markup inside them gives no links.
    emitter.naively_switch_states(true);
    let mut links = Vec::new();
    let mut in_style = false;
    for token in Tokenizer::new_with_emitter(html, emitter).flatten() {
        match token {
            Token::StartTag(tag) => {
                in_style = tag.name == b"style";
                for name in [b"src".as_slice(), b"href"] {
                    if let Some(v) = tag.attributes.get(name) {
                        let link = String::from_utf8_lossy(&v.value).into_owned();
                        links.push((line(html, v.span.start), link));
                    }
                }
            }
            Token::EndTag(_) => in_style = false,
            Token::String(text) if in_style => {
                let css = String::from_utf8_lossy(&text.value);
                links.extend(css_links(&css, line(html, text.span.start)));
            }
            _ => {}
        }
    }
    links
}

/// (line, link) for each url(...). first_line is the line of the first byte.
fn css_links(css: &str, first_line: usize) -> Vec<(usize, String)> {
    CSS_URL
        .captures_iter(css)
        .filter_map(|c| c.get(1).or(c.get(2)).or(c.get(3)))
        .map(|m| {
            (
                first_line + line(css, m.start()) - 1,
                m.as_str().to_string(),
            )
        })
        .collect()
}

/// The line (from 1) of a byte offset.
fn line(text: &str, offset: usize) -> usize {
    let end = offset.min(text.len());
    text.as_bytes()[..end]
        .iter()
        .filter(|&&b| b == b'\n')
        .count()
        + 1
}

/// The warning for one link in the file `from`, or None.
fn problem(from: &str, link: &str, files: &HashSet<&str>) -> Option<String> {
    let link = link.trim(); // as the URL parser does
    if link.is_empty() || link.starts_with('#') || link.starts_with("//") || SCHEME.is_match(link) {
        return None;
    }
    if link.starts_with('/') {
        return Some(format!(
            "{link} starts with \"/\". It goes to the top of the domain, not to this Site."
        ));
    }
    let path = link.split(['?', '#']).next().unwrap_or_default();
    if path.is_empty() {
        return None; // "?page=2" is this page
    }
    let Some(target) = resolve(from, &decode(path)) else {
        return Some(format!("{link} goes above the top of the Site."));
    };
    // "about" is fine for the folder "about/": the server redirects to "about/".
    if files.contains(target.as_str()) || files.contains(format!("{target}/index.html").as_str()) {
        return None;
    }
    // On Windows the browser finds Photo.png for photo.png; the server does not.
    match files.iter().find(|f| f.eq_ignore_ascii_case(&target)) {
        Some(f) => Some(format!(
            "{link} is not in the upload. The file is {f}: the case is different."
        )),
        None => Some(format!("{link} is not in the upload.")),
    }
}

/// The file that `link` in the file `from` points to, from the root. A link to
/// a folder ("img/", ".", "..") means its index.html. None above the root.
fn resolve(from: &str, link: &str) -> Option<String> {
    let mut parts: Vec<&str> = from.split('/').collect();
    parts.pop(); // the name of `from`
    let mut folder = false;
    for seg in link.split('/') {
        folder = matches!(seg, "" | "." | "..");
        match seg {
            "" | "." => {}
            ".." => {
                parts.pop()?;
            }
            s => parts.push(s),
        }
    }
    if folder {
        parts.push("index.html");
    }
    Some(parts.join("/"))
}

/// Decodes %XX escapes; "%20" is a space. A bad escape stays as it is.
fn decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%'
            && let Some(hex) = s.get(i + 1..i + 3)
            && hex.bytes().all(|c| c.is_ascii_hexdigit())
        {
            out.push(u8::from_str_radix(hex, 16).unwrap());
            i += 3;
            continue;
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn links(pairs: &[(usize, &str)]) -> Vec<(usize, String)> {
        pairs.iter().map(|&(l, s)| (l, s.to_string())).collect()
    }

    #[test]
    fn html_links_reads_src_and_href_but_not_comments_or_scripts() {
        let html = "<!doctype html>\n\
                    <!-- <a href=\"comment.html\"> -->\n\
                    <img src=a.png>\n\
                    <script>let s = '<a href=\"script.html\">';</script>\n\
                    <a\n  \
                    href=\"b.html\">B</a>\n\
                    <style>\n\
                    body { background: url('c.png') }\n\
                    </style>\n";
        assert_eq!(
            html_links(html),
            links(&[(3, "a.png"), (6, "b.html"), (8, "c.png")])
        );
    }

    #[test]
    fn css_links_reads_url_with_and_without_quotes() {
        let css = "a { b: url(x.png) }\nc { d: url( \"y.png\" ) }\ne { f: URL('z.png') }";
        assert_eq!(
            css_links(css, 5),
            links(&[(5, "x.png"), (6, "y.png"), (7, "z.png")])
        );
    }

    #[test]
    fn problem_finds_root_links_and_missing_files() {
        let files: HashSet<&str> = [
            "index.html",
            "a/page.html",
            "a/img/photo.png",
            "a/x y.png",
            "about/index.html",
        ]
        .into();
        let p = |link| problem("a/page.html", link, &files);

        for fine in [
            "",
            "#top",
            "?page=2",
            "https://example.com/x.png",
            "//cdn.example.com/x.js",
            "mailto:me@example.com",
            "img/photo.png?v=2#top",
            " img/photo.png ",
            "x%20y.png",
            "../index.html",
            "..",
            "../about",
            "../about/",
        ] {
            assert_eq!(p(fine), None, "{fine:?}");
        }
        assert!(p("/img/photo.png").unwrap().contains("starts with \"/\""));
        assert!(p("missing.png").is_some());
        assert!(p("./").is_some()); // a/index.html
        assert!(p("../../x.png").is_some());
        // The exact name, for a link with the wrong case.
        assert!(p("img/Photo.png").unwrap().contains("a/img/photo.png"));
    }
}
