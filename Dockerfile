# syntax=docker/dockerfile:1
FROM golang:1.27.3-bookworm AS build

WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/shelf-server . \
    && go build -trimpath -ldflags="-s -w" -o /out/logout-all ./cmd/logout-all \
    && mkdir -m 0700 /out/data

FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /app
COPY --from=build /out/shelf-server /app/shelf-server
COPY --from=build /out/logout-all /app/logout-all
# Docker copies this ownership to a new named volume on its first use.
COPY --from=build --chown=65532:65532 /out/data /data

ENV DATA_DIR=/data PORT=8080
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/shelf-server"]
