# Pinned to a patch release on purpose: a floating "golang:1.27" tag can resolve to a
# stale cached layer and quietly ship a binary built with a vulnerable standard
# library. Bump this when `govulncheck ./...` (see README) reports fixes.
FROM golang:1.27.2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN mkdir -p /data/media /data/backups && chmod 700 /data/backups

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/server /server
# writable by the nonroot user; fresh named volumes inherit this ownership
COPY --from=build --chown=nonroot:nonroot /data /data
USER nonroot
ENTRYPOINT ["/server"]
