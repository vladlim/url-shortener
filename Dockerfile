FROM golang:1.25.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/url-shortener ./cmd

FROM build AS test
RUN apk add --no-cache gcc musl-dev
COPY migrations/ ./migrations/
CMD ["go", "test", "-race", "-v", "-count=1", "-timeout=2m", "./..."]

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/url-shortener ./url-shortener
COPY configs/config.yaml ./configs/config.yaml
USER app
EXPOSE 8082
ENTRYPOINT ["/app/url-shortener"]
