# Test MCP login locally

The auth base lets an MCP client sign in through Shelf's existing GitHub login.
It exposes one read-only tool, `shelf_status`. The tool confirms the Owner's MCP connection and access-token expiry.
Upload and other Site tools are not implemented in this base.

Use the official MCP Inspector as the test client. No custom login script is needed.
You start Shelf and the MCP. Inspector handles the browser login and calls the protected tool.

## Prepare the local settings

Keep your existing GitHub client settings in `server/.env`. Do not paste keys into a chat or commit them.
For this test, the existing GitHub OAuth App must allow this callback:

```text
http://localhost:8080/auth/github/callback
```

Use `ADMIN_URL=http://localhost:8080` and `FILES_URL=http://files.localhost:8080` for the local Shelf server.
The GitHub callback and the MCP client's callback are different addresses.

The server adds the OAuth tables through a migration on its next start.
Before the migration, it creates and checks a consistent SQLite backup in `DATA_DIR`.
The file name starts with `shelf-before-migration-`.
If the backup fails, startup stops before the migration.
This local backup does not replace an off-server backup.

## Start Shelf in terminal 1

From the repo root:

```powershell
cd server
$env:MCP_RESOURCE_URL = 'http://localhost:8081/mcp'
$env:MCP_CLIENT_ID = 'shelf-mcp'
$env:MCP_REDIRECT_URIS = 'http://127.0.0.1:6276/oauth/callback'
go run .
```

The OAuth routes stay disabled when `MCP_RESOURCE_URL` is unset.
`MCP_REDIRECT_URIS` is a comma-separated list of exact callback URLs. Wildcards, queries, and fragments are refused.

## Start the MCP in terminal 2

From the repo root:

```powershell
cd cli
$env:SHELF_URL = 'http://localhost:8080'
$env:OWNER_GITHUB_ID = '<your numeric GitHub ID>'
cargo run -- mcp
```

Set the numeric ID to the same account as Shelf's Owner setting.
This is an account ID, not a GitHub token. The Rust process does not read `server/.env` or need the GitHub client secret.
It does not use a CLI key for this auth-only status tool.

The defaults are:

| Setting | Default | Purpose |
|---|---|---|
| `SHELF_MCP_URL` | `http://localhost:8081/mcp` | The resource URL in OAuth requests and tokens. Must match `MCP_RESOURCE_URL`. |
| `SHELF_MCP_BIND` | `127.0.0.1:8081` | The local listener. |
| `SHELF_URL` | `http://localhost:8080` | The trusted Shelf issuer. |
| `OWNER_GITHUB_ID` | Required | The allowed Owner account. |
| `SHELF_MCP_ALLOWED_ORIGINS` | MCP's own origin | Optional comma-separated browser origins. No wildcard origins. |

Plain HTTP is allowed only on loopback hosts. Remote URLs require HTTPS.
Host and Origin checks reject requests addressed to another host or sent from an unapproved web origin.
Native clients, including Inspector's CLI, do not need an Origin header.

## Test with MCP Inspector in terminal 3

Inspector 2.15.0 needs a supported Node version: `^22.23.4`, `^24.22.1`, or `>=26.12.1`.
The command below supplies Node 26.12.1 through npm's package cache for this run.

From the repo root, use a separate test profile and keep credentials in memory:

```powershell
$env:MCP_INSPECTOR_SECRET_STORE = 'memory'
$env:MCP_INSPECTOR_PERSIST_TOKENS = 'none'
$env:MCP_STORAGE_DIR = Join-Path (Get-Location) '.scratch/mcp-inspector'
$env:MCP_INSPECTOR_OAUTH_STATE_PATH = Join-Path $env:MCP_STORAGE_DIR 'oauth.json'
$env:MCP_CLIENT_CONFIG_PATH = Join-Path $env:MCP_STORAGE_DIR 'client.json'
npm exec --yes --cache .scratch/npm-cache --package=node@26.12.1 --package=@modelcontextprotocol/inspector@2.15.0 -- mcp-inspector --cli --server-url http://localhost:8081/mcp --client-id shelf-mcp --callback-url http://127.0.0.1:6276/oauth/callback --method tools/call --tool-name shelf_status
```

Inspector opens the login page. Sign in with GitHub if asked, then select **Approve** on Shelf's consent page.
If the browser does not open, use the authorization URL Inspector prints in your terminal.
The result must include `authenticated: true`, your numeric Owner ID, and the access-token expiry.

To check tool discovery, repeat the command with `--method tools/list` and omit `--tool-name shelf_status`.
Memory-only credentials are not retained between CLI runs, so each run may ask for login again.
Inspector may create non-secret profile metadata under `.scratch/mcp-inspector`.
After the test, open `http://localhost:8080/oauth/connections` in the Admin page and revoke the test connection.
The old grant stops working after revocation. A new Inspector run may ask you to authorize a new connection.

These commands test a standard client login and a tool call. They do not by themselves test refresh rotation.
The automated auth tests cover refresh rotation, replay rejection, expiry, and revocation.

Reference: [Inspector CLI OAuth options](https://github.com/modelcontextprotocol/inspector/blob/main/clients/cli/README.md#oauth),
[Inspector credential storage](https://github.com/modelcontextprotocol/inspector/blob/main/docs/environment-variables.md#secret-store).

## How the auth works

1. The Rust MCP publishes protected-resource metadata at `/.well-known/oauth-protected-resource/mcp`.
2. Shelf publishes OAuth server metadata at `/.well-known/oauth-authorization-server` on the admin host.
3. The client sends an authorization request with its registered ID, exact callback URL, MCP resource URL, and an S256 PKCE challenge.
4. Shelf uses the existing GitHub login and Owner check, then shows an explicit approval page.
5. The client exchanges a one-use code and its PKCE verifier for an access token and refresh token.
6. The MCP checks the opaque access token with Shelf's `/oauth/session` endpoint on every protected request. It checks the issuer, Owner ID, audience, scope, and expiry of the result.

Codes last one minute. Pending login requests last ten minutes.
Access tokens last up to 15 minutes. A grant lasts 30 days from the first exchange.
Refresh rotates the refresh token. It does not extend the grant's 30-day limit.
Reuse of an old refresh token revokes the whole grant.
Both tokens stop working when the grant is revoked.
Only hashes of credentials are stored in SQLite.

The Rust process sends access tokens only to the configured Shelf issuer, over HTTPS or local loopback HTTP.
It does not follow redirects or cache accepted tokens. If Shelf cannot check a token, the MCP refuses the request.
`/oauth/session` accepts only the token in the Authorization header. Browser cookies and CLI keys do not grant MCP access.

The authorization server supports preconfigured public clients with PKCE. It does not provide dynamic client registration.
For a future ChatGPT connection, configure the client ID and exact callback URLs in advance.
The local test does not establish that a scheduled ChatGPT task can write without approval.
ChatGPT cannot reach a loopback endpoint on your computer. A remote test needs HTTPS hosting or an MCP tunnel.

GitHub login sessions, CLI keys, and MCP grants are separate. Logging out of the Admin page does not revoke MCP grants.
Use **Manage MCP connections** on the Admin page to revoke them.

## Run the checks

```powershell
cd server
go vet ./...
go test ./...
cd ../cli
cargo fmt --check
cargo clippy --all-targets --locked -- -D warnings
cargo test --locked
```
