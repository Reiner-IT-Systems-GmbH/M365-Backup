# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/m365backup ./cmd/server

# Root in image so bind-mounted host paths are writable without chmod gymnastics.
# Prefer a dedicated host user + chown for hardened deploys.
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/m365backup /app/m365backup
ENV HTTP_ADDR=:8080 \
    KOPIA_ROOT=/data/kopia \
    STAGING_ROOT=/data/staging \
    DB_DRIVER=mysql
EXPOSE 8080
ENTRYPOINT ["/app/m365backup"]
