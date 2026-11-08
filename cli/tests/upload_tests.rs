use std::collections::VecDeque;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::process::{Command, Output, Stdio};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use serde_json::json;
use tempfile::TempDir;

#[derive(Debug)]
struct Request {
    line: String,
    headers: String,
    body: Vec<u8>,
}

// A local HTTP fixture for CLI tests. Each connection gets one reply and closes.
struct Server {
    url: String,
    requests: Arc<Mutex<Vec<Request>>>,
    stopped: Arc<AtomicBool>,
    worker: Option<JoinHandle<()>>,
}

impl Server {
    fn new(replies: Vec<Option<String>>) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        listener.set_nonblocking(true).unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let requests = Arc::new(Mutex::new(Vec::new()));
        let stopped = Arc::new(AtomicBool::new(false));
        let captured = requests.clone();
        let stop = stopped.clone();
        let worker = thread::spawn(move || {
            let mut replies = VecDeque::from(replies);
            while !stop.load(Ordering::Relaxed) {
                match listener.accept() {
                    Ok((mut stream, _)) => {
                        // Windows can inherit the listener's nonblocking mode.
                        stream.set_nonblocking(false).unwrap();
                        captured.lock().unwrap().push(read_request(&mut stream));
                        if let Some(reply) = replies.pop_front().expect("unexpected extra request")
                        {
                            stream.write_all(reply.as_bytes()).unwrap();
                        }
                    }
                    Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(5))
                    }
                    Err(e) => panic!("accept: {e}"),
                }
            }
        });
        Self {
            url,
            requests,
            stopped,
            worker: Some(worker),
        }
    }

    fn run(&self, source: &std::path::Path, args: &[&str]) -> Output {
        Command::new(env!("CARGO_BIN_EXE_shelf"))
            .arg("upload")
            .arg(source)
            .arg("demo")
            .args(args)
            .env("SHELF_URL", &self.url)
            .env("SHELF_KEY", "shelf_test_key")
            .env("NO_COLOR", "1")
            .env("NO_PROXY", "*")
            .env("no_proxy", "*")
            .env_remove("HTTP_PROXY")
            .env_remove("HTTPS_PROXY")
            .env_remove("ALL_PROXY")
            .env_remove("http_proxy")
            .env_remove("https_proxy")
            .env_remove("all_proxy")
            .stdin(Stdio::null())
            .output()
            .unwrap()
    }
}

impl Drop for Server {
    fn drop(&mut self) {
        self.stopped.store(true, Ordering::Relaxed);
        if let Some(worker) = self.worker.take() {
            let result = worker.join();
            if !thread::panicking() {
                result.unwrap();
            }
        }
    }
}

fn read_request(stream: &mut TcpStream) -> Request {
    stream
        .set_read_timeout(Some(Duration::from_secs(5)))
        .unwrap();
    let mut reader = BufReader::new(stream);
    let mut line = String::new();
    reader.read_line(&mut line).unwrap();
    let mut headers = String::new();
    let mut size = 0;
    loop {
        let mut header = String::new();
        assert!(
            reader.read_line(&mut header).unwrap() > 0,
            "request ended inside headers"
        );
        if header == "\r\n" {
            break;
        }
        if let Some(n) = header.to_ascii_lowercase().strip_prefix("content-length:") {
            size = n.trim().parse().unwrap();
        }
        headers.push_str(&header.to_ascii_lowercase());
    }
    let mut body = vec![0; size];
    reader.read_exact(&mut body).unwrap();
    Request {
        line,
        headers,
        body,
    }
}

fn reply(status: u16, body: &str) -> Option<String> {
    Some(format!(
        "HTTP/1.1 {status} Test\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    ))
}

fn site(visibility: &str, archived: bool) -> serde_json::Value {
    json!({"path":"demo", "kind":"file", "file":"notes.txt", "visibility":visibility,
        "archived":archived, "updated_at":1800000000, "url":"https://files.example.com/demo"})
}

fn file(contents: &str) -> (TempDir, std::path::PathBuf) {
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("notes.txt");
    std::fs::write(&source, contents).unwrap();
    (dir, source)
}

fn output(result: &Output) -> String {
    format!(
        "{}{}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    )
}

#[test]
fn creates_a_site_with_the_checked_archive() {
    let server = Server::new(vec![
        reply(200, r#"{"taken":false,"site":null}"#),
        reply(200, &site("private", false).to_string()),
    ]);
    let (_dir, source) = file("hello");
    let result = server.run(&source, &[]);
    assert!(result.status.success(), "{}", output(&result));
    let requests = server.requests.lock().unwrap();
    assert_eq!(requests.len(), 2);
    assert_eq!(requests[0].line, "GET /api/v1/sites/demo HTTP/1.1\r\n");
    assert_eq!(
        requests[1].line,
        "PUT /api/v1/sites/demo?kind=file HTTP/1.1\r\n"
    );
    assert!(
        requests[1]
            .headers
            .contains("authorization: bearer shelf_test_key\r\n")
    );
    assert!(
        requests[1]
            .headers
            .contains("content-type: application/gzip\r\n")
    );
    assert!(
        requests[1]
            .headers
            .contains(&format!("content-length: {}\r\n", requests[1].body.len()))
    );
    let mut archive = tar::Archive::new(flate2::read::GzDecoder::new(&requests[1].body[..]));
    let mut entries = archive.entries().unwrap();
    let mut entry = entries.next().unwrap().unwrap();
    assert_eq!(entry.path().unwrap().to_str(), Some("notes.txt"));
    let mut data = String::new();
    entry.read_to_string(&mut data).unwrap();
    assert_eq!(data, "hello");
    assert!(entries.next().is_none());
    assert!(output(&result).contains("https://files.example.com/demo"));
    assert!(output(&result).contains("private"));
}

#[test]
fn replacement_needs_confirmation_and_yes_preserves_the_displayed_state() {
    let existing = json!({"taken":true,"site":site("public",true)}).to_string();
    let (_dir, source) = file("hello");
    let server = Server::new(vec![reply(200, &existing)]);
    let result = server.run(&source, &[]);
    assert!(!result.status.success());
    assert!(output(&result).contains("--yes"), "{}", output(&result));
    assert!(output(&result).contains("public"));
    assert!(output(&result).contains("Archived"));
    assert_eq!(server.requests.lock().unwrap().len(), 1);
    let server = Server::new(vec![
        reply(200, &existing),
        reply(200, &site("public", true).to_string()),
    ]);
    let result = server.run(&source, &["--yes"]);
    assert!(result.status.success(), "{}", output(&result));
    assert!(output(&result).contains("Archived"));
    assert!(output(&result).contains("cache"));
}

#[test]
fn warnings_and_hard_errors_stop_before_any_request() {
    let server = Server::new(vec![]);
    let dir = TempDir::new().unwrap();
    let source = dir.path().join("page.html");
    std::fs::write(&source, "<a href='/other'>other</a>").unwrap();
    let result = server.run(&source, &[]);
    assert!(!result.status.success());
    assert!(output(&result).contains("--yes"), "{}", output(&result));
    std::fs::write(
        &source,
        "shelf_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    )
    .unwrap();
    let result = server.run(&source, &["--yes"]);
    assert!(!result.status.success());
    assert!(
        output(&result).contains("--allow-secrets"),
        "{}",
        output(&result)
    );
    assert!(server.requests.lock().unwrap().is_empty());
}

#[test]
fn lost_or_invalid_success_reply_has_an_unknown_result_without_replay() {
    let (_dir, source) = file("hello");
    for last in [
        None,
        reply(200, "{}"),
        reply(
            200,
            &site("private", false).to_string().replace("demo", "other"),
        ),
    ] {
        let server = Server::new(vec![reply(200, r#"{"taken":false,"site":null}"#), last]);
        let result = server.run(&source, &["--yes"]);
        assert!(!result.status.success());
        assert!(
            output(&result).contains("may already have changed"),
            "{}",
            output(&result)
        );
        assert!(!output(&result).contains("Uploaded"));
        assert_eq!(server.requests.lock().unwrap().len(), 2);
    }
}

#[test]
fn server_refusal_is_reported_without_success_or_replay() {
    let (_dir, source) = file("hello");
    let server = Server::new(vec![
        reply(200, r#"{"taken":false,"site":null}"#),
        reply(
            503,
            r#"{"error":{"code":"upload_busy","message":"Another upload is in progress."}}"#,
        ),
    ]);
    let result = server.run(&source, &["--yes"]);
    assert!(!result.status.success());
    assert!(output(&result).contains("Another upload"));
    assert!(!output(&result).contains("Uploaded"));
    assert_eq!(server.requests.lock().unwrap().len(), 2);
}

#[test]
fn upload_redirect_is_not_followed() {
    let (_dir, source) = file("hello");
    let server = Server::new(vec![reply(200, r#"{"taken":false,"site":null}"#), Some("HTTP/1.1 307 Temporary Redirect\r\nLocation: /redirected\r\nContent-Length: 0\r\nConnection: close\r\n\r\n".into())]);
    let result = server.run(&source, &["--yes"]);
    assert!(!result.status.success());
    assert_eq!(server.requests.lock().unwrap().len(), 2);
}

#[test]
fn yes_and_allow_secrets_upload_a_folder_with_warnings() {
    let dir = TempDir::new().unwrap();
    std::fs::write(dir.path().join("index.html"), "<a href='/other'>other</a>").unwrap();
    std::fs::write(
        dir.path().join("app.js"),
        "shelf_kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk",
    )
    .unwrap();
    let mut folder = site("private", false);
    folder["kind"] = json!("folder");
    folder["file"] = json!("");
    folder["url"] = json!("https://files.example.com/demo/");
    let server = Server::new(vec![
        reply(200, r#"{"taken":false,"site":null}"#),
        reply(200, &folder.to_string()),
    ]);
    let result = server.run(dir.path(), &["--yes", "--allow-secrets"]);
    assert!(result.status.success(), "{}", output(&result));
    let requests = server.requests.lock().unwrap();
    assert_eq!(
        requests[1].line,
        "PUT /api/v1/sites/demo?kind=folder HTTP/1.1\r\n"
    );
}

#[test]
fn invalid_path_check_or_unauthorized_key_never_uploads() {
    let (_dir, source) = file("hello");
    for response in [
        reply(
            401,
            r#"{"error":{"code":"unauthorized","message":"Log in with: shelf login"}}"#,
        ),
        reply(200, r#"{"taken":true,"site":null}"#),
        reply(200, "<html>proxy error</html>"),
    ] {
        let server = Server::new(vec![response]);
        let result = server.run(&source, &["--yes"]);
        assert!(!result.status.success());
        assert!(!output(&result).contains("Uploaded"));
        assert_eq!(server.requests.lock().unwrap().len(), 1);
    }
}

#[test]
fn proxy_error_and_server_failure_after_sending_are_unknown() {
    let (_dir, source) = file("hello");
    for response in [
        reply(502, "<html>Bad gateway</html>"),
        reply(
            500,
            r#"{"error":{"code":"server_error","message":"Something went wrong."}}"#,
        ),
    ] {
        let server = Server::new(vec![reply(200, r#"{"taken":false,"site":null}"#), response]);
        let result = server.run(&source, &["--yes"]);
        assert!(!result.status.success());
        assert!(
            output(&result).contains("may already have changed"),
            "{}",
            output(&result)
        );
        assert_eq!(server.requests.lock().unwrap().len(), 2);
    }
}
