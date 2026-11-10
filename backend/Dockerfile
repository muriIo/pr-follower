FROM golang:1.27-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Migrations are embedded (//go:embed in internal/infrastructure/database), so the binary is self-contained.
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 app && adduser -S -u 10001 -G app app
COPY --from=build --chown=app:app /out/server /usr/local/bin/server
USER 10001
ENV PORT=8080
EXPOSE 8080
CMD ["server"]
