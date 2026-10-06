# The app's image (anetos new): one static binary on a distroless base,
# run as a user without privileges. See the "Deploy" guide.
#
#   docker build -t tracker --build-arg VERSION=v1.0.0 .
#   docker run --rm --env-file production.env tracker migrate
#   docker run -d -p 8080:8080 --env-file production.env -v tracker-data:/data tracker
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
    go tool anetos build -o /out/tracker --version="$VERSION" && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/tracker /app/tracker
# /data holds the files of STORAGE_DRIVER=local and the SQLite database:
# mount a volume there.
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_ENV=production \
    HTTP_ADDR=:8080 \
    STORAGE_ROOT=/data/storage \
    DB_DATABASE=/data/app.db
EXPOSE 8080
USER 65532:65532
# The server's readiness; turn it off for containers that don't serve
# HTTP (run --only=workers).
HEALTHCHECK --interval=30s --timeout=10s --start-period=15s CMD ["/app/tracker", "health:check"]
ENTRYPOINT ["/app/tracker"]
# Every component; run --only=http, workers, scheduler or listeners for one role.
CMD ["run"]
