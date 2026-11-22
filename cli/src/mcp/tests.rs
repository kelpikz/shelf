use super::*;
use axum::body::{Body, to_bytes};
use axum::http::Request;
use tower::ServiceExt;

fn settings() -> Settings {
    Settings::parse(
        "http://localhost:8081/mcp",
        "127.0.0.1:8081",
        "http://localhost:8080",
        "1",
        "",
    )
    .unwrap()
}

#[test]
fn configuration_refuses_remote_http_and_unsafe_urls() {
    for url in [
        "http://evil.example/mcp",
        "https://mcp.example/mcp?token=secret",
        "https://name:password@mcp.example/mcp",
        "https://mcp.example/other",
    ] {
        assert!(Settings::parse(url, "127.0.0.1:8081", "http://localhost:8080", "1", "").is_err());
    }
    assert!(
        Settings::parse(
            "http://localhost:8081/mcp",
            "127.0.0.1:8081",
            "http://evil.example",
            "1",
            ""
        )
        .is_err()
    );
    assert!(
        Settings::parse(
            "http://localhost:8081/mcp",
            "127.0.0.1:8081",
            "http://localhost:8080",
            "",
            ""
        )
        .is_err()
    );
}

#[tokio::test]
async fn discovery_is_public_but_cookie_only_and_bad_hosts_cannot_call_tools() {
    let app = router(settings()).unwrap();
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/.well-known/oauth-protected-resource/mcp")
                .header("host", "localhost:8081")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let metadata: serde_json::Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 8192).await.unwrap()).unwrap();
    assert_eq!(metadata["resource"], "http://localhost:8081/mcp");
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/mcp")
                .method("POST")
                .header("host", "localhost:8081")
                .header("cookie", "shelf_session=not-a-token")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(
        response.headers()["www-authenticate"]
            .to_str()
            .unwrap()
            .contains("resource_metadata=")
    );
    for (header, value) in [("host", "evil.example"), ("origin", "https://evil.example")] {
        let mut request = Request::builder()
            .uri("/mcp")
            .header("host", "localhost:8081")
            .body(Body::empty())
            .unwrap();
        request.headers_mut().insert(header, value.parse().unwrap());
        assert_eq!(
            app.clone().oneshot(request).await.unwrap().status(),
            StatusCode::FORBIDDEN
        );
    }
}

#[test]
fn token_identity_must_match_issuer_owner_audience_expiry_and_scope() {
    let config = settings();
    let valid = || Identity {
        iss: config.issuer.clone(),
        sub: "1".into(),
        aud: config.resource.clone(),
        scope: "shelf:read".into(),
        expires_at: crate::now() + 60,
    };
    assert!(config.accepts(&valid()));
    for field in ["iss", "sub", "aud", "scope", "expires"] {
        let mut identity = valid();
        match field {
            "iss" => identity.iss = "https://evil.example".into(),
            "sub" => identity.sub = "2".into(),
            "aud" => identity.aud = "other".into(),
            "scope" => identity.scope = "shelf:reader".into(),
            _ => identity.expires_at = crate::now() - 1,
        }
        assert!(!config.accepts(&identity), "accepted wrong {field}");
    }
}

#[tokio::test]
async fn authenticated_client_can_discover_and_call_status_then_revocation_blocks_it() {
    use axum::routing::get;
    use std::sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    };
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let issuer = format!("http://127.0.0.1:{}", listener.local_addr().unwrap().port());
    let config = Settings::parse(
        "http://localhost:8081/mcp",
        "127.0.0.1:8081",
        &issuer,
        "1",
        "",
    )
    .unwrap();
    let issuer_copy = issuer.clone();
    let revoked = Arc::new(AtomicBool::new(false));
    let revoked_copy = revoked.clone();
    let fake=Router::new().route("/oauth/session",get(move |headers:axum::http::HeaderMap|{
        let revoked=revoked_copy.clone();let issuer=issuer_copy.clone();
        async move {
            if revoked.load(Ordering::SeqCst)||headers.get("authorization").and_then(|h|h.to_str().ok())!=Some("Bearer test-opaque-token") {return StatusCode::UNAUTHORIZED.into_response()}
            Json(json!({"iss":issuer,"sub":"1","aud":"http://localhost:8081/mcp","scope":"shelf:read","expires_at":crate::now()+60})).into_response()
        }
    }));
    let server = tokio::spawn(async move { axum::serve(listener, fake).await.unwrap() });
    let app = router(config).unwrap();
    for payload in [
        json!({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}),
        json!({"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}),
        json!({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"shelf_status","arguments":{}}}),
    ] {
        let request = Request::builder()
            .method("POST")
            .uri("/mcp")
            .header("host", "localhost:8081")
            .header("authorization", "Bearer test-opaque-token")
            .header("content-type", "application/json")
            .header("accept", "application/json, text/event-stream")
            .header("mcp-protocol-version", "2025-06-18")
            .body(Body::from(payload.to_string()))
            .unwrap();
        let response = app.clone().oneshot(request).await.unwrap();
        let status = response.status();
        let body = to_bytes(response.into_body(), 32768).await.unwrap();
        assert_eq!(status, StatusCode::OK, "{}", String::from_utf8_lossy(&body));
        let result: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert!(result.get("error").is_none(), "{result}");
        assert!(!String::from_utf8_lossy(&body).contains("test-opaque-token"));
        if payload["method"] == "tools/list" {
            assert_eq!(result["result"]["tools"][0]["name"], "shelf_status")
        }
        if payload["method"] == "tools/call" {
            assert_eq!(result["result"]["structuredContent"]["authenticated"], true)
        }
    }
    revoked.store(true, Ordering::SeqCst);
    let request = Request::builder()
        .uri("/mcp")
        .method("POST")
        .header("host", "localhost:8081")
        .header("authorization", "Bearer test-opaque-token")
        .body(Body::empty())
        .unwrap();
    assert_eq!(
        app.oneshot(request).await.unwrap().status(),
        StatusCode::UNAUTHORIZED
    );
    server.abort();
}
