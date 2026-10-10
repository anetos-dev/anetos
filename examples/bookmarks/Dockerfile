# The app's image (anetos new): one static binary on a distroless base,
# run as a user without privileges. See the "Deploy" guide.
#
#   docker build -t bookmarks --build-arg VERSION=v1.0.0 .
#   docker run -d -p 8080:8080 --env-file production.env -v bookmarks-data:/data bookmarks
#
# It migrates the SQLite database in /data when it starts
# (DB_MIGRATE_ON_START); the server is ready once that's done.
#
# The settings come from the environment (production.env: see
# deploy/production.env.example); .env isn't in the image. RUN --mount
# needs BuildKit, Docker's builder since version 23.

FROM golang:1.26 AS build
WORKDIR /src
# The dependencies first: in BuildKit's cache, kept between builds on
# the same machine.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# The version the app's version command prints (the image has no git history).
ARG VERSION=""
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go tool anetos build -o /out/bookmarks --version="$VERSION" && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/bookmarks /app/bookmarks
# /data holds the files of STORAGE_DRIVER=local and the SQLite database:
# mount a volume there.
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_ENV=production \
    STORAGE_ROOT=/data/storage \
    DB_NAME=/data/app.db \
    DB_MIGRATE_ON_START=true
EXPOSE 8080
USER 65532:65532
# The server's readiness; turn it off for containers that don't serve
# HTTP (run --only=worker).
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s CMD ["/app/bookmarks", "health:check"]
ENTRYPOINT ["/app/bookmarks"]
# Every component; run --only=web, worker, scheduler or listener for one process type.
CMD ["run"]
