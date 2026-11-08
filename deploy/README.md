# Run shelf with Docker

Run these commands from the repo root on the Linux droplet.
Install Docker Engine and the Compose plugin first.
Keep the existing nginx on the host.

## Set the config

```sh
cp server/env.prod.example .env
chmod 600 .env
```

Edit `.env` and replace the example Owner and GitHub values.
Use the Owner's numeric GitHub ID and verified email address.
Set both URLs to the public HTTPS addresses, with no path or trailing slash.
Set the GitHub OAuth app callback URL to:

```text
https://files-admin.ajitha.fyi/auth/github/callback
```

The example config caps total Site file bytes at 1 GiB.
The server reserves space for a full 50 MiB upload before it accepts another upload.
It also leaves at least 1 GiB of free disk space, plus space for file and folder entries.
Adjust `SITE_STORAGE_MAX_BYTES` and `UPLOAD_MIN_FREE_BYTES` for your disk.
The Site quota excludes SQLite and container logs. It is not a limit on the Docker volume itself.

Compose fixes `DATA_DIR=/data` and the container port at `8080`.
The named volume `shelf_data` holds SQLite and Site files.
A new volume starts empty. The local `server/data` folder does not move into it.
Run only one shelf container with this volume.

## Build the image

```sh
docker compose config --quiet
docker compose build --pull shelf
```

The build includes the server and the `logout-all` recovery tool.
The Rust CLI stays on your computer.
Build on a machine with the same CPU architecture as the droplet.
If the 1 GB droplet cannot build the image, build it on another Linux Docker host:

```sh
docker compose build --pull shelf
docker image save -o /tmp/shelf-image.tar shelf:local
```

Copy `/tmp/shelf-image.tar` to the droplet outside the repo, then load it:

```sh
docker image load -i /path/to/shelf-image.tar
```

Copy the Compose file and your private `.env` to the droplet too.
The start command below uses the image you built or loaded.

## Create the container

To create the container and volume without starting the server:

```sh
docker compose create --no-build shelf
docker compose ps --all
```

For a new local setup, copy `server/env.local.example` to `server/.env` and fill in the values.
Keep your existing `server/.env` if it is already configured.
For local Docker use, add `--env-file server/.env` after `docker compose` in each command.
The local volume starts empty. It does not copy your existing development data.

## Connect your proxy

Configure nginx separately on the droplet.
The container publishes HTTP at `127.0.0.1:8080` on the Docker host.
Forward both shelf hosts there and preserve the original `Host` header.
Use the public HTTPS addresses in `.env`.

The upload API accepts up to 10 MiB and has a 120-second deadline, plus 30 seconds to send an error response.
Allow for these limits in your proxy. Disable automatic upload retries and preserve the server's cache headers.

## Start and check shelf

These commands start the server. Run them when you are ready to deploy.

```sh
docker compose up -d --no-build shelf
docker compose logs --tail=50 shelf
curl --fail http://127.0.0.1:8080/healthz
```

The health request returns `ok`. This checks local HTTP access, not GitHub login or uploads.
After you configure nginx, check `/healthz` through both public HTTPS hosts too.
Open the Admin page and complete GitHub login.
Run `shelf login` on your computer, then upload a test Site.
Check replacement, Public and Private Visibility, archive, unarchive, and delete.
The signed-in Owner can open Private and Archived Sites. Visitors get 404 for both.
Log out of the Admin page and confirm that Owner viewing access ends.

The container runs as UID and GID `65532`. Its root filesystem is read-only.
Only `/data` is writable. A fresh named volume gets the required ownership from the image.
If you use an existing volume, check its ownership before you start the container.

## Stop or update shelf

The volume exists outside the container. A replacement container mounts the same `shelf_data` volume at `/data`.
On the same Docker host, a redeploy keeps the database and Site files.
Keep the Compose project name `shelf` to use this volume.
An image rebuild does not change the stored data.
The new server can change data after it starts, so back up before any future schema migration.

A different Docker host has different volumes. Transfer the data separately if you move to another droplet.
Docker Desktop stores local volumes in its Linux virtual machine. These volumes do not sync to the droplet.

To stop the server and keep its data:

```sh
docker compose down
```

Do not add `--volumes` or `-v`. Those flags delete the database and all Site files.

To update, build or load the new `shelf:local` image, then run:

```sh
docker compose up -d --no-build shelf
docker compose logs --tail=50 shelf
```

Expect a short outage during the update.
Before a future schema migration, stop shelf and back up the complete data volume.
If the backup fails, stop the update. Do not copy only `shelf.db` while SQLite is running.

To end all browser sessions with the existing recovery tool:

```sh
docker compose exec shelf /app/logout-all
```

This command changes the database. It also ends the linked Owner viewing sessions.
It does not cancel CLI keys.

## References

- [Docker Compose service settings](https://docs.docker.com/reference/compose-file/services/)
- [Distroless runtime images](https://github.com/GoogleContainerTools/distroless)
