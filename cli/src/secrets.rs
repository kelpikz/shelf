//! Secret check: well-known key and token formats in the files of an upload,
//! from the Gitleaks default rules. A match gives the rule, the file, and the
//! line, never the secret.

use std::fs;
use std::path::Path;
use std::sync::LazyLock;

use anyhow::{Context, Result};
use regex::bytes::Regex;

/// (rule name, pattern). Patterns follow gitleaks.toml (ids in the comments),
/// without its end-of-token checks. Stripe test keys are left out.
const RULES: [(&str, &str); 9] = [
    // private-key
    (
        "a private key",
        r"(?i)-----BEGIN[ A-Z0-9_-]{0,100}PRIVATE KEY(?: BLOCK)?-----[\s\S-]{64,}?KEY(?: BLOCK)?-----",
    ),
    // aws-access-token
    (
        "an AWS access key",
        r"\b(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}\b",
    ),
    // github-pat, github-oauth, github-app-token, github-refresh-token,
    // github-fine-grained-pat
    (
        "a GitHub token",
        r"(?:ghp|gho|ghu|ghs|ghr)_[0-9a-zA-Z]{36}|github_pat_\w{82}",
    ),
    // stripe-access-token
    (
        "a Stripe key",
        r"\b(?:sk|rk)_(?:live|prod)_[a-zA-Z0-9]{10,99}",
    ),
    // slack-bot-token, slack-user-token, slack-app-token
    (
        "a Slack token",
        r"xoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*|xox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9-]{28,34}|(?i:xapp-\d-[A-Z0-9]+-\d+-[a-z0-9]+)",
    ),
    // slack-webhook-url
    (
        "a Slack webhook",
        r"hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9+/]{43,56}",
    ),
    // openai-api-key. T3BlbkFJ is "OpenAI" in base64.
    (
        "an OpenAI key",
        r"sk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{58,74}T3BlbkFJ[A-Za-z0-9_-]{58,74}|sk-[a-zA-Z0-9]{20}T3BlbkFJ[a-zA-Z0-9]{20}",
    ),
    // anthropic-api-key, anthropic-admin-api-key
    (
        "an Anthropic key",
        r"sk-ant-(?:api03|admin01)-[a-zA-Z0-9_-]{93}AA",
    ),
    // Not from gitleaks.toml: shelf's own CLI key, "shelf_" and 32 random
    // bytes in base64url (server/internal/auth).
    ("a shelf CLI key", r"\bshelf_[A-Za-z0-9_-]{43}"),
];

static COMPILED: LazyLock<Vec<(&str, Regex)>> = LazyLock::new(|| {
    RULES
        .iter()
        .map(|&(name, re)| (name, Regex::new(re).unwrap()))
        .collect()
});

/// "file:line: ..." for each match in the files.
pub fn check(root: &Path, files: &[String]) -> Result<Vec<String>> {
    let mut found = Vec::new();
    for f in files {
        let data = fs::read(root.join(f)).with_context(|| format!("Cannot read {f}"))?;
        for (line, rule) in find(&data) {
            found.push(format!("{f}:{line}: this looks like {rule}."));
        }
    }
    Ok(found)
}

/// (line, rule name) for each match, in the order of the rules.
fn find(data: &[u8]) -> Vec<(usize, &'static str)> {
    let mut found = Vec::new();
    for (name, re) in COMPILED.iter() {
        for m in re.find_iter(data) {
            let line = data[..m.start()].iter().filter(|&&b| b == b'\n').count() + 1;
            found.push((line, *name));
        }
    }
    found
}

#[cfg(test)]
mod tests {
    use super::*;

    // Test values are built at run time, so that no scanner (GitHub push
    // protection, for example) finds a token in this file.

    #[test]
    fn find_matches_each_rule() {
        let hits = [
            (
                "a private key",
                format!(
                    "-----BEGIN RSA {0}-----\n{1}\n-----END RSA {0}-----",
                    "PRIVATE KEY",
                    "A".repeat(64)
                ),
            ),
            ("an AWS access key", format!("AKIA{}", "Q".repeat(16))),
            ("a GitHub token", format!("ghp_{}", "a".repeat(36))),
            ("a GitHub token", format!("github_pat_{}", "a".repeat(82))),
            ("a Stripe key", format!("sk_live_{}", "a".repeat(24))),
            (
                "a Slack token",
                format!("xoxb-{}-{}-abc", "1".repeat(12), "2".repeat(12)),
            ),
            (
                "a Slack webhook",
                format!("https://hooks.slack.com/services/{}", "A".repeat(44)),
            ),
            (
                "an OpenAI key",
                format!("sk-proj-{}T3BlbkFJ{}", "a".repeat(58), "b".repeat(58)),
            ),
            (
                "an Anthropic key",
                format!("sk-ant-api03-{}AA", "a".repeat(93)),
            ),
            ("a shelf CLI key", format!("shelf_{}", "a".repeat(43))),
        ];
        for (rule, value) in hits {
            let text = format!("x\nconst k = \"{value}\";\n");
            assert_eq!(find(text.as_bytes()), [(2, rule)], "{rule}");
        }
    }

    #[test]
    fn find_ignores_near_misses() {
        let misses = [
            "-----BEGIN PUBLIC KEY-----".to_string(),
            format!("AKIA{}", "Q".repeat(15)),
            format!("ghp_{}", "a".repeat(35)),
            format!("sk_test_{}", "a".repeat(24)), // a Stripe test key
            format!("sk-{}", "a".repeat(48)),      // no OpenAI marker
            "shelf_upload_button".to_string(),
        ];
        for value in misses {
            assert!(find(value.as_bytes()).is_empty(), "{value}");
        }
    }
}
